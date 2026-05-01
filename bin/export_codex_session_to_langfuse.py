#!/usr/bin/env python3
"""Export visible Codex session text into Langfuse trace input/output fields."""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
import re
import sys
import time
import tomllib
import urllib.error
import urllib.request
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path
from typing import Any


DEFAULT_ENVIRONMENT = "default"
DEFAULT_SERVICE_NAME = "codex_transcript_exporter"
MAX_FIELD_CHARS = 50_000
SECRET_PATTERNS = (
    (re.compile(r"Basic [A-Za-z0-9+/=]{32,}"), "Basic <redacted>"),
    (re.compile(r"sk-lf-[A-Za-z0-9-]+"), "sk-lf-<redacted>"),
    (re.compile(r"pk-lf-[A-Za-z0-9-]+"), "pk-lf-<redacted>"),
    (re.compile(r"sk-or-v1-[A-Za-z0-9]+"), "sk-or-v1-<redacted>"),
    (re.compile(r"gsk_[A-Za-z0-9]+"), "gsk_<redacted>"),
    (re.compile(r"gh[pousr]_[A-Za-z0-9_]+"), "gh<redacted>"),
    (re.compile(r"(?i)(api[_-]?key|secret[_-]?key|access[_-]?token|bearer[_-]?token)([\"' :=]+)([A-Za-z0-9_./+=:-]{16,})"), r"\1\2<redacted>"),
)


@dataclass
class LangfuseConfig:
    host: str
    public_key: str
    secret_key: str


@dataclass
class Turn:
    session_id: str
    turn_id: str
    trace_id: str
    start_ts: str
    end_ts: str
    cwd: str | None = None
    model: str | None = None
    user_messages: list[str] = field(default_factory=list)
    assistant_messages: list[str] = field(default_factory=list)
    token_usage: dict[str, Any] | None = None
    observations: list["Observation"] = field(default_factory=list)

    @property
    def input_text(self) -> str:
        return "\n\n".join(message.strip() for message in self.user_messages if message.strip()).strip()

    @property
    def output_text(self) -> str:
        return "\n\n".join(message.strip() for message in self.assistant_messages if message.strip()).strip()


@dataclass
class Observation:
    name: str
    timestamp: str
    input_text: str = ""
    output_text: str = ""
    metadata: dict[str, Any] = field(default_factory=dict)


def append_unique(values: list[str], value: str | None) -> None:
    if not value:
        return
    value = value.strip()
    if value and value not in values:
        values.append(value)


def iso_to_ns(value: str) -> str:
    dt = datetime.fromisoformat(value.replace("Z", "+00:00"))
    return str(int(dt.timestamp() * 1_000_000_000))


def load_config(config_path: Path) -> LangfuseConfig:
    cfg: dict[str, Any] = {}
    if config_path.exists():
        cfg = tomllib.loads(config_path.read_text(encoding="utf-8"))

    mcp_env = cfg.get("mcp_servers", {}).get("langfuse", {}).get("env", {})
    resolved_host = mcp_env.get("LANGFUSE_HOST")
    resolved_public = mcp_env.get("LANGFUSE_PUBLIC_KEY")
    resolved_secret = mcp_env.get("LANGFUSE_SECRET_KEY")

    missing = [
        name
        for name, value in (
            ("host", resolved_host),
            ("public key", resolved_public),
            ("secret key", resolved_secret),
        )
        if not value
    ]
    if missing:
        raise ValueError(f"Missing Langfuse {'/'.join(missing)} in [mcp_servers.langfuse.env] in {config_path}")

    return LangfuseConfig(
        host=str(resolved_host).rstrip("/"),
        public_key=str(resolved_public),
        secret_key=str(resolved_secret),
    )


def codex_home() -> Path:
    return Path(os.environ.get("CODEX_HOME", Path.home() / ".codex"))


def default_config_path() -> Path:
    return codex_home() / "config.toml"


def redact_text(value: str) -> str:
    for pattern, replacement in SECRET_PATTERNS:
        value = pattern.sub(replacement, value)
    return value


def limit_text(value: str, max_chars: int = MAX_FIELD_CHARS) -> str:
    if len(value) <= max_chars:
        return value
    return value[:max_chars] + f"\n\n[truncated to {max_chars} characters]"


def export_text(value: str | None) -> str:
    return limit_text(redact_text(value or ""))


def stable_json(value: Any) -> str:
    if value is None:
        return ""
    if isinstance(value, str):
        return value
    return json.dumps(value, ensure_ascii=False, indent=2, sort_keys=True, default=str)


def format_command(command: Any) -> str:
    if isinstance(command, list):
        if len(command) >= 3 and command[-2] == "-lc":
            return str(command[-1])
        return " ".join(str(part) for part in command)
    return stable_json(command)


def add_observation(
    turn: Turn,
    name: str,
    timestamp: str | None,
    input_text: str = "",
    output_text: str = "",
    metadata: dict[str, Any] | None = None,
) -> None:
    if not input_text and not output_text:
        return
    turn.observations.append(
        Observation(
            name=name,
            timestamp=timestamp or turn.end_ts or turn.start_ts,
            input_text=input_text,
            output_text=output_text,
            metadata=metadata or {},
        )
    )


def command_output(payload: dict[str, Any]) -> str:
    parts: list[str] = []
    for label, key in (("formatted output", "formatted_output"), ("output", "aggregated_output"), ("stdout", "stdout"), ("stderr", "stderr")):
        value = payload.get(key)
        if value:
            parts.append(f"## {label}\n{stable_json(value)}")
    return "\n\n".join(parts)


def patch_output(payload: dict[str, Any]) -> str:
    parts: list[str] = []
    if payload.get("stdout"):
        parts.append(f"## stdout\n{payload['stdout']}")
    if payload.get("stderr"):
        parts.append(f"## stderr\n{payload['stderr']}")
    changes = payload.get("changes") or {}
    for path, change in changes.items():
        if not isinstance(change, dict):
            continue
        parts.append(f"## {path} ({change.get('type', 'change')})")
        if change.get("move_path"):
            parts.append(f"moved to: {change['move_path']}")
        if change.get("unified_diff"):
            parts.append(f"```diff\n{change['unified_diff']}\n```")
        elif change.get("content"):
            parts.append(f"```text\n{change['content']}\n```")
    return "\n\n".join(parts)


def file_change_metadata(changes: dict[str, Any]) -> dict[str, Any]:
    changed_files: list[str] = []
    added_files: list[str] = []
    modified_files: list[str] = []
    deleted_files: list[str] = []
    moved_files: list[str] = []
    file_change_types: dict[str, str] = {}

    for path, change in changes.items():
        if not isinstance(change, dict):
            continue
        change_type = str(change.get("type") or "change")
        changed_files.append(path)
        file_change_types[path] = change_type

        if change_type == "add":
            added_files.append(path)
        elif change_type == "delete":
            deleted_files.append(path)
        elif change_type == "update":
            modified_files.append(path)
        else:
            modified_files.append(path)

        if change.get("move_path"):
            moved_files.append(f"{path} -> {change['move_path']}")

    return {
        "changed_files": changed_files,
        "added_files": added_files,
        "modified_files": modified_files,
        "deleted_files": deleted_files,
        "moved_files": moved_files,
        "file_change_types": file_change_types,
        "changed_file_count": len(changed_files),
    }


def metadata_without_large_fields(payload: dict[str, Any], exclude: set[str]) -> dict[str, Any]:
    metadata: dict[str, Any] = {}
    for key, value in payload.items():
        if key in exclude:
            continue
        if isinstance(value, (dict, list)):
            metadata[key] = stable_json(value)
        else:
            metadata[key] = value
    return metadata


def find_session_by_id(session_id: str, root: Path) -> Path:
    matches = sorted(root.glob(f"sessions/**/rollout-*{session_id}.jsonl"))
    if not matches:
        matches = sorted(root.glob(f"sessions/**/rollout-*{session_id}*.jsonl"))
    if not matches:
        raise FileNotFoundError(f"No Codex rollout JSONL found for session id {session_id}")
    if len(matches) > 1:
        raise RuntimeError("Multiple Codex rollout files matched; pass --path explicitly")
    return matches[0]


def latest_session(root: Path) -> Path:
    matches = list(root.glob("sessions/**/rollout-*.jsonl"))
    if not matches:
        raise FileNotFoundError(f"No Codex rollout JSONL files found under {root / 'sessions'}")
    return max(matches, key=lambda path: path.stat().st_mtime)


def rollout_start_timestamp(path: Path) -> float | None:
    parts = path.name.split("-")
    if len(parts) < 4 or parts[0] != "rollout":
        return None
    try:
        started = datetime.strptime("-".join(parts[1:4]), "%Y-%m-%dT%H-%M-%S")
    except ValueError:
        return None
    return started.timestamp()


def session_files(root: Path, marker: Path | None = None) -> list[Path]:
    marker_time = marker.stat().st_mtime if marker and marker.exists() else None
    paths: list[Path] = []

    for path in root.glob("sessions/**/rollout-*.jsonl"):
        if marker_time is not None:
            started_at = rollout_start_timestamp(path)
            if started_at is not None and started_at + 1 < marker_time:
                continue
            try:
                if path.stat().st_mtime + 1 < marker_time:
                    continue
            except OSError:
                continue
        paths.append(path)

    return sorted(paths)


def text_from_content(content: Any, text_type: str) -> str | None:
    if not isinstance(content, list):
        return None
    parts = [
        item.get("text", "")
        for item in content
        if isinstance(item, dict) and item.get("type") == text_type and item.get("text")
    ]
    return "\n".join(parts).strip() or None


def parse_turns(session_path: Path) -> list[Turn]:
    session_id = ""
    session_model = None
    session_cwd = None
    current_turn_id: str | None = None
    turns_by_id: dict[str, Turn] = {}
    pending_calls: dict[str, dict[str, Any]] = {}
    covered_call_ids: set[str] = set()

    for line_number, raw in enumerate(session_path.read_text(encoding="utf-8").splitlines(), start=1):
        if not raw.strip():
            continue
        try:
            item = json.loads(raw)
        except json.JSONDecodeError as exc:
            raise ValueError(f"{session_path}:{line_number} is not valid JSON: {exc}") from exc

        item_type = item.get("type")
        timestamp = item.get("timestamp")
        payload = item.get("payload") or {}

        if item_type == "session_meta":
            session_id = str(payload.get("id") or session_id)
            session_model = payload.get("model") or payload.get("default_model") or session_model
            session_cwd = payload.get("cwd") or session_cwd
            continue

        if item_type == "turn_context":
            turn_id = str(payload.get("turn_id") or "")
            trace_id = str(payload.get("trace_id") or "")
            if not turn_id or not trace_id:
                current_turn_id = None
                continue
            current_turn_id = turn_id
            turns_by_id[turn_id] = Turn(
                session_id=session_id,
                turn_id=turn_id,
                trace_id=trace_id,
                start_ts=timestamp or datetime.utcnow().isoformat(timespec="milliseconds") + "Z",
                end_ts=timestamp or datetime.utcnow().isoformat(timespec="milliseconds") + "Z",
                cwd=payload.get("cwd") or session_cwd,
                model=payload.get("model") or session_model,
            )
            continue

        if not current_turn_id or current_turn_id not in turns_by_id:
            continue

        turn = turns_by_id[current_turn_id]
        if item_type == "event_msg":
            payload_type = payload.get("type")
            if payload_type == "user_message":
                append_unique(turn.user_messages, payload.get("message"))
            elif payload_type == "agent_message" and payload.get("phase") == "final_answer":
                append_unique(turn.assistant_messages, payload.get("message"))
                turn.end_ts = timestamp or turn.end_ts
            elif payload_type == "agent_message":
                add_observation(
                    turn,
                    "codex.message.commentary",
                    timestamp,
                    output_text=payload.get("message") or "",
                    metadata={"phase": payload.get("phase")},
                )
            elif payload_type == "exec_command_end":
                call_id = str(payload.get("call_id") or "")
                covered_call_ids.add(call_id)
                add_observation(
                    turn,
                    "codex.tool.exec_command",
                    timestamp,
                    input_text=format_command(payload.get("command")),
                    output_text=command_output(payload),
                    metadata=metadata_without_large_fields(
                        payload,
                        {
                            "command",
                            "stdout",
                            "stderr",
                            "aggregated_output",
                            "formatted_output",
                            "parsed_cmd",
                        },
                    ),
                )
            elif payload_type == "patch_apply_end":
                call_id = str(payload.get("call_id") or "")
                covered_call_ids.add(call_id)
                patch_input = ""
                pending = pending_calls.get(call_id)
                if pending:
                    patch_input = stable_json(pending.get("input") or pending.get("arguments"))
                metadata = metadata_without_large_fields(payload, {"stdout", "stderr", "changes"})
                metadata.update(file_change_metadata(payload.get("changes") or {}))
                add_observation(
                    turn,
                    "codex.tool.apply_patch",
                    timestamp,
                    input_text=patch_input,
                    output_text=patch_output(payload),
                    metadata=metadata,
                )
            elif payload_type == "mcp_tool_call_end":
                call_id = str(payload.get("call_id") or "")
                covered_call_ids.add(call_id)
                add_observation(
                    turn,
                    "codex.tool.mcp",
                    timestamp,
                    input_text=stable_json(payload.get("invocation")),
                    output_text=stable_json(payload.get("result")),
                    metadata=metadata_without_large_fields(payload, {"invocation", "result"}),
                )
            elif payload_type == "web_search_end":
                call_id = str(payload.get("call_id") or "")
                covered_call_ids.add(call_id)
                add_observation(
                    turn,
                    "codex.tool.web_search",
                    timestamp,
                    input_text=stable_json({"query": payload.get("query"), "action": payload.get("action")}),
                    output_text=stable_json(payload.get("action")),
                    metadata=metadata_without_large_fields(payload, {"query", "action"}),
                )
            elif payload_type == "task_complete":
                append_unique(turn.assistant_messages, payload.get("last_agent_message"))
                turn.end_ts = timestamp or turn.end_ts
            elif payload_type == "token_count":
                info = payload.get("info") or {}
                usage = info.get("last_token_usage") or info.get("total_token_usage")
                if usage:
                    turn.token_usage = usage
            continue

        if item_type == "response_item":
            role = payload.get("role")
            if payload.get("type") == "message" and role == "user":
                append_unique(turn.user_messages, text_from_content(payload.get("content"), "input_text"))
            elif payload.get("type") == "message" and role == "assistant" and payload.get("phase") == "final_answer":
                append_unique(turn.assistant_messages, text_from_content(payload.get("content"), "output_text"))
                turn.end_ts = timestamp or turn.end_ts
            elif payload.get("type") in {"function_call", "custom_tool_call", "tool_search_call"}:
                call_id = str(payload.get("call_id") or "")
                if call_id:
                    pending_calls[call_id] = payload
            elif payload.get("type") in {"function_call_output", "custom_tool_call_output", "tool_search_output"}:
                call_id = str(payload.get("call_id") or "")
                if call_id in covered_call_ids:
                    continue
                pending = pending_calls.get(call_id, {})
                name = pending.get("name") or payload.get("type", "tool").removesuffix("_output")
                observation_name = "codex.tool.tool_search" if payload.get("type") == "tool_search_output" else f"codex.tool.{name}"
                add_observation(
                    turn,
                    observation_name,
                    timestamp,
                    input_text=stable_json(pending.get("arguments") or pending.get("input") or pending.get("execution")),
                    output_text=stable_json(payload.get("output") or payload.get("tools")),
                    metadata={
                        "call_id": call_id,
                        "response_item_type": payload.get("type"),
                        "status": payload.get("status"),
                    },
                )

    return list(turns_by_id.values())


def usage_details(turn: Turn) -> dict[str, int] | None:
    if not turn.token_usage:
        return None
    usage = {
        "input": turn.token_usage.get("input_tokens"),
        "output": turn.token_usage.get("output_tokens"),
        "total": turn.token_usage.get("total_tokens"),
    }
    return {key: int(value) for key, value in usage.items() if isinstance(value, int)}


def otlp_attributes(turn: Turn, environment: str) -> list[dict[str, Any]]:
    attrs: list[dict[str, Any]] = [
        {"key": "langfuse.trace.name", "value": {"stringValue": "codex.turn.transcript"}},
        {"key": "langfuse.trace.input", "value": {"stringValue": export_text(turn.input_text)}},
        {"key": "langfuse.trace.output", "value": {"stringValue": export_text(turn.output_text)}},
        {"key": "langfuse.session.id", "value": {"stringValue": turn.session_id}},
        {"key": "langfuse.environment", "value": {"stringValue": environment}},
        {"key": "langfuse.observation.type", "value": {"stringValue": "span"}},
        {"key": "langfuse.observation.input", "value": {"stringValue": json.dumps(export_text(turn.input_text))}},
        {"key": "langfuse.observation.output", "value": {"stringValue": json.dumps(export_text(turn.output_text))}},
        {"key": "langfuse.trace.metadata.codex_session_id", "value": {"stringValue": turn.session_id}},
        {"key": "langfuse.trace.metadata.codex_turn_id", "value": {"stringValue": turn.turn_id}},
        {"key": "langfuse.trace.metadata.codex_transcript_exported", "value": {"boolValue": True}},
        {"key": "langfuse.observation.metadata.session_id", "value": {"stringValue": turn.session_id}},
        {"key": "langfuse.observation.metadata.turn_id", "value": {"stringValue": turn.turn_id}},
    ]

    if turn.cwd:
        attrs.append({"key": "langfuse.observation.metadata.cwd", "value": {"stringValue": turn.cwd}})
    if turn.model:
        attrs.append({"key": "langfuse.observation.metadata.model", "value": {"stringValue": turn.model}})
    usage = usage_details(turn)
    if usage:
        attrs.append({"key": "langfuse.observation.usage_details", "value": {"stringValue": json.dumps(usage)}})
    return attrs


def observation_attrs(turn: Turn, observation: Observation, environment: str) -> list[dict[str, Any]]:
    attrs: list[dict[str, Any]] = [
        {"key": "langfuse.trace.name", "value": {"stringValue": "codex.turn.transcript"}},
        {"key": "langfuse.trace.input", "value": {"stringValue": export_text(turn.input_text)}},
        {"key": "langfuse.trace.output", "value": {"stringValue": export_text(turn.output_text)}},
        {"key": "langfuse.session.id", "value": {"stringValue": turn.session_id}},
        {"key": "langfuse.environment", "value": {"stringValue": environment}},
        {"key": "langfuse.trace.metadata.codex_session_id", "value": {"stringValue": turn.session_id}},
        {"key": "langfuse.trace.metadata.codex_turn_id", "value": {"stringValue": turn.turn_id}},
        {"key": "langfuse.trace.metadata.codex_transcript_exported", "value": {"boolValue": True}},
        {"key": "langfuse.observation.type", "value": {"stringValue": "span"}},
        {"key": "langfuse.observation.input", "value": {"stringValue": json.dumps(export_text(observation.input_text))}},
        {"key": "langfuse.observation.output", "value": {"stringValue": json.dumps(export_text(observation.output_text))}},
    ]
    if observation.metadata:
        attrs.append(
            {
                "key": "langfuse.observation.metadata",
                "value": {"stringValue": json.dumps(observation.metadata, ensure_ascii=False, sort_keys=True, default=str)},
            }
        )
    return attrs


def timeline_observation(turn: Turn) -> Observation | None:
    if not turn.observations:
        return None

    parts: list[str] = []
    for index, observation in enumerate(turn.observations, start=1):
        parts.append(f"## {index}. {observation.name}")
        if observation.input_text:
            parts.append(f"Input:\n{observation.input_text}")
        if observation.output_text:
            parts.append(f"Output:\n{observation.output_text}")
    return Observation(
        name="codex.timeline",
        timestamp=turn.end_ts,
        output_text="\n\n".join(parts),
        metadata={"event_count": len(turn.observations), "turn_id": turn.turn_id},
    )


def observation_span(turn: Turn, observation: Observation, environment: str, key: str) -> dict[str, Any]:
    span_id = hashlib.sha256(f"codex-observation:{turn.trace_id}:{turn.turn_id}:{key}".encode()).hexdigest()[:16]
    return {
        "traceId": turn.trace_id,
        "spanId": span_id,
        "name": observation.name,
        "kind": 1,
        "startTimeUnixNano": iso_to_ns(observation.timestamp),
        "endTimeUnixNano": iso_to_ns(observation.timestamp),
        "attributes": observation_attrs(turn, observation, environment),
    }


def build_payload(turn: Turn, environment: str, service_name: str) -> dict[str, Any]:
    span_id = hashlib.sha256(f"codex-transcript:{turn.trace_id}:{turn.turn_id}".encode()).hexdigest()[:16]
    spans = [
        {
            "traceId": turn.trace_id,
            "spanId": span_id,
            "name": "codex.transcript",
            "kind": 1,
            "startTimeUnixNano": iso_to_ns(turn.start_ts),
            "endTimeUnixNano": iso_to_ns(turn.end_ts),
            "attributes": otlp_attributes(turn, environment),
        }
    ]
    for index, observation in enumerate(turn.observations):
        spans.append(observation_span(turn, observation, environment, str(index)))
    timeline = timeline_observation(turn)
    if timeline:
        spans.append(observation_span(turn, timeline, environment, "timeline"))

    return {
        "resourceSpans": [
            {
                "resource": {
                    "attributes": [
                        {"key": "service.name", "value": {"stringValue": service_name}},
                        {"key": "langfuse.environment", "value": {"stringValue": environment}},
                    ]
                },
                "scopeSpans": [
                    {
                        "scope": {"name": "codex-transcript-exporter", "version": "0.1.0"},
                        "spans": spans,
                    }
                ],
            }
        ]
    }


def build_observation_payload(turn: Turn, observation: Observation, environment: str, service_name: str, key: str) -> dict[str, Any]:
    return {
        "resourceSpans": [
            {
                "resource": {
                    "attributes": [
                        {"key": "service.name", "value": {"stringValue": service_name}},
                        {"key": "langfuse.environment", "value": {"stringValue": environment}},
                    ]
                },
                "scopeSpans": [
                    {
                        "scope": {"name": "codex-transcript-exporter", "version": "0.1.0"},
                        "spans": [observation_span(turn, observation, environment, key)],
                    }
                ],
            }
        ]
    }


def auth_header(config: LangfuseConfig) -> str:
    token = base64.b64encode(f"{config.public_key}:{config.secret_key}".encode("utf-8")).decode("ascii")
    return f"Basic {token}"


def post_otlp(config: LangfuseConfig, payload: dict[str, Any]) -> int:
    request = urllib.request.Request(
        f"{config.host}/api/public/otel/v1/traces",
        data=json.dumps(payload).encode("utf-8"),
        headers={
            "Content-Type": "application/json",
            "Authorization": auth_header(config),
            "x-langfuse-ingestion-version": "4",
        },
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            response.read()
            return int(response.status)
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"Langfuse OTLP export failed with HTTP {exc.code}: {body[:500]}") from exc


def export_turn(config: LangfuseConfig, turn: Turn, environment: str, service_name: str) -> int:
    return post_otlp(config, build_payload(turn, environment, service_name))


def fetch_trace(config: LangfuseConfig, trace_id: str) -> dict[str, Any]:
    request = urllib.request.Request(
        f"{config.host}/api/public/traces/{trace_id}",
        headers={"Authorization": auth_header(config)},
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return json.loads(response.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"Langfuse trace fetch failed with HTTP {exc.code}: {body[:500]}") from exc


def verify_trace_io(config: LangfuseConfig, turn: Turn, timeout_seconds: float, interval_seconds: float) -> tuple[bool, bool]:
    deadline = time.monotonic() + max(timeout_seconds, 0.0)
    last_result = (False, False)
    last_error: Exception | None = None

    while True:
        try:
            trace = fetch_trace(config, turn.trace_id)
            last_error = None
            observation_matches = [
                observation
                for observation in trace.get("observations") or []
                if observation.get("name") == "codex.transcript"
            ]
            last_result = (
                trace.get("input") == export_text(turn.input_text)
                or any(observation.get("input") == export_text(turn.input_text) for observation in observation_matches),
                trace.get("output") == export_text(turn.output_text)
                or any(observation.get("output") == export_text(turn.output_text) for observation in observation_matches),
            )
        except RuntimeError as exc:
            last_error = exc

        if all(last_result):
            return last_result

        if time.monotonic() >= deadline:
            if last_error is not None:
                raise last_error
            return last_result

        time.sleep(max(interval_seconds, 0.1))


def preview(value: str, max_chars: int = 120) -> str:
    value = value.replace("\n", "\\n")
    return value if len(value) <= max_chars else value[: max_chars - 3] + "..."


def export_session_turns(
    config: LangfuseConfig,
    session_path: Path,
    environment: str,
    service_name: str,
    turn_id: str | None = None,
    quiet: bool = False,
) -> list[Turn]:
    turns = parse_turns(session_path)
    if turn_id:
        turns = [turn for turn in turns if turn.turn_id == turn_id]
    exportable = [turn for turn in turns if turn.trace_id and turn.input_text and turn.output_text]

    if not exportable:
        if not quiet:
            print(f"No completed Codex turns with visible input/output found in {session_path}", file=sys.stderr)
        return []

    if not quiet:
        print(f"session_file={session_path}")
        for turn in exportable:
            print(
                f"turn={turn.turn_id} trace={turn.trace_id} "
                f"input={preview(export_text(turn.input_text))!r} "
                f"output={preview(export_text(turn.output_text))!r} "
                f"observations={len(turn.observations)}"
            )

    for turn in exportable:
        status = export_turn(config, turn, environment, service_name)
        if not quiet:
            print(f"exported trace={turn.trace_id} status={status}")

    return exportable


def watch_sessions(
    config: LangfuseConfig,
    root: Path,
    marker: Path | None,
    environment: str,
    service_name: str,
    poll_interval_seconds: float,
    watch_timeout_seconds: float,
    quiet: bool,
) -> int:
    exported_turns: set[tuple[str, str]] = set()
    exported_observations: set[tuple[str, str, int]] = set()
    deadline = time.monotonic() + max(watch_timeout_seconds, 0.0)

    while True:
        for session_path in session_files(root, marker):
            try:
                turns = parse_turns(session_path)
            except (OSError, ValueError):
                continue

            for turn in turns:
                if not turn.trace_id:
                    continue

                for index, observation in enumerate(turn.observations):
                    observation_key = (turn.trace_id, turn.turn_id, index)
                    if observation_key in exported_observations:
                        continue
                    post_otlp(
                        config,
                        build_observation_payload(
                            turn,
                            observation,
                            environment,
                            service_name,
                            str(index),
                        ),
                    )
                    exported_observations.add(observation_key)
                    if not quiet:
                        print(f"watch_exported_observation trace={turn.trace_id} turn={turn.turn_id} name={observation.name}")

                key = (turn.trace_id, turn.turn_id)
                if key in exported_turns or not turn.input_text or not turn.output_text:
                    continue
                export_turn(config, turn, environment, service_name)
                exported_turns.add(key)
                if not quiet:
                    print(f"watch_exported trace={turn.trace_id} turn={turn.turn_id}")

        if time.monotonic() >= deadline:
            return 0
        time.sleep(max(poll_interval_seconds, 0.25))


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Backfill visible Codex prompt/answer text into matching Langfuse traces."
    )
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument("--session-id", help="Codex session id from `codex resume <id>`")
    source.add_argument("--path", type=Path, help="Path to a Codex rollout JSONL file")
    source.add_argument("--latest", action="store_true", help="Export the latest Codex rollout JSONL file")
    source.add_argument("--watch", action="store_true", help="Poll Codex rollout files and export turns as they complete")
    parser.add_argument("--turn-id", help="Only export one turn id from the selected session")
    parser.add_argument("--start-after-marker", type=Path, help="Only watch sessions started or modified after this file")
    parser.add_argument("--config", type=Path, default=default_config_path())
    parser.add_argument("--environment", default=DEFAULT_ENVIRONMENT)
    parser.add_argument("--service-name", default=DEFAULT_SERVICE_NAME)
    parser.add_argument("--quiet", action="store_true", help="Only print errors")
    parser.add_argument("--no-verify", action="store_true", help="Do not fetch traces after export")
    parser.add_argument("--verify-wait-seconds", type=float, default=25.0)
    parser.add_argument("--verify-interval-seconds", type=float, default=3.0)
    parser.add_argument("--poll-interval-seconds", type=float, default=2.0)
    parser.add_argument("--watch-timeout-seconds", type=float, default=43_200.0)
    args = parser.parse_args()

    try:
        config = load_config(args.config)
        if args.watch:
            return watch_sessions(
                config,
                codex_home(),
                args.start_after_marker.expanduser() if args.start_after_marker else None,
                args.environment,
                args.service_name,
                args.poll_interval_seconds,
                args.watch_timeout_seconds,
                args.quiet,
            )

        if args.path:
            session_path = args.path.expanduser()
        elif args.latest:
            session_path = latest_session(codex_home())
        else:
            session_path = find_session_by_id(args.session_id, codex_home())

        exportable = export_session_turns(
            config,
            session_path,
            args.environment,
            args.service_name,
            args.turn_id,
            args.quiet,
        )
        if not exportable:
            return 1

        if not args.no_verify:
            for turn in exportable:
                has_input, has_output = verify_trace_io(
                    config,
                    turn,
                    args.verify_wait_seconds,
                    args.verify_interval_seconds,
                )
                if not args.quiet:
                    print(f"verified trace={turn.trace_id} input={has_input} output={has_output}")
                if not has_input or not has_output:
                    print(
                        f"ERROR: trace {turn.trace_id} did not show exported input/output before timeout",
                        file=sys.stderr,
                    )
                    return 1
        return 0
    except Exception as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
