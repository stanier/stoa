#!/usr/bin/env python3
"""
test-audio-tts.py — smoke test for LFM2.5-Audio TTS via llama.cpp router.

Tries three strategies in order, stopping at the first that returns audio:

  1. POST /v1/audio/speech  (OpenAI-compat; requires model= in body)
  2. POST /tts              (llama.cpp native endpoint, if router exposes it)
  3. POST /v1/chat/completions  with modalities=["audio"]  (chat-based TTS)

Strategy 1 was previously 404-ing because the model alias "lfm2.5-audio" was
used; the canonical model ID "lfm2.5_audio-1.5b_q8-0" is tried here.
Strategy 3 is a fallback — some llama.cpp builds don't honour `modalities`
through the router and will return a text response instead of audio.

Usage:
    python3 scripts/test-audio-tts.py [--url URL] [--text TEXT] [--out FILE]
    python3 scripts/test-audio-tts.py --strategy 1|2|3

Defaults:
    url   https://192.168.1.152:8080
    model lfm2.5_audio-1.5b_q8-0
    text  "Hello from stoa. The voice integration is working."
    out   /tmp/tts-output.wav
"""

import argparse
import base64
import json
import shutil
import subprocess
import ssl
import sys
import urllib.error
import urllib.request

DEFAULT_URL   = "https://192.168.1.152:8080"
DEFAULT_MODEL = "lfm2.5_audio-1.5b_q8-0"
DEFAULT_TEXT  = "Hello from stoa. The voice integration is working."
DEFAULT_OUT   = "/tmp/tts-output.wav"


def _ssl_ctx() -> ssl.SSLContext:
    ctx = ssl.create_default_context()
    ctx.check_hostname = False
    ctx.verify_mode    = ssl.CERT_NONE
    return ctx


def _post(url: str, body: bytes, content_type: str) -> tuple[int, bytes]:
    req = urllib.request.Request(
        url, data=body,
        headers={"Content-Type": content_type},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, context=_ssl_ctx()) as resp:
            return resp.status, resp.read()
    except urllib.error.HTTPError as e:
        return e.code, e.read()


def _save_and_report(data: bytes, path: str) -> None:
    with open(path, "wb") as f:
        f.write(data)
    print(f"  saved  : {path}  ({len(data)} bytes)")
    if shutil.which("ffprobe"):
        r = subprocess.run(
            ["ffprobe", "-v", "quiet", "-show_entries", "format=duration",
             "-of", "csv=p=0", path],
            capture_output=True, text=True,
        )
        if r.stdout.strip():
            print(f"  duration: {r.stdout.strip()} s")


# ---------------------------------------------------------------------------
# Strategy 1 — /v1/audio/speech  (OpenAI-compatible)
# ---------------------------------------------------------------------------

def strategy_openai_speech(base: str, model: str, text: str, voice: str, out: str) -> bool:
    url  = base + "/v1/audio/speech"
    body = json.dumps({"model": model, "input": text, "voice": voice}).encode()
    print(f"[1] POST {url}")
    status, raw = _post(url, body, "application/json")
    print(f"    status: {status}")
    if status == 200 and raw[:4] in (b"RIFF", b"OggS", b"ID3\x03", b"\xff\xfb", b"\xff\xf3"):
        print("    result: audio bytes returned")
        _save_and_report(raw, out)
        return True
    # 200 but JSON error, or 4xx/5xx
    try:
        err = json.loads(raw)
        print(f"    result: {json.dumps(err)}")
    except Exception:
        print(f"    result: {raw[:200]!r}")
    return False


# ---------------------------------------------------------------------------
# Strategy 2 — /tts  (llama.cpp native)
# ---------------------------------------------------------------------------

def strategy_native_tts(base: str, model: str, text: str, out: str) -> bool:
    url  = base + "/tts"
    body = json.dumps({"model": model, "text": text}).encode()
    print(f"[2] POST {url}")
    status, raw = _post(url, body, "application/json")
    print(f"    status: {status}")
    if status == 200 and len(raw) > 44:
        # Heuristic: raw audio bytes or JSON with base64 data
        try:
            j = json.loads(raw)
            audio_b64 = j.get("audio") or j.get("data") or j.get("wav")
            if audio_b64:
                audio = base64.b64decode(audio_b64)
                print("    result: base64 audio in JSON response")
                _save_and_report(audio, out)
                return True
        except Exception:
            pass
        if raw[:4] in (b"RIFF", b"OggS", b"ID3\x03", b"\xff\xfb", b"\xff\xf3"):
            print("    result: raw audio bytes returned")
            _save_and_report(raw, out)
            return True
    try:
        err = json.loads(raw)
        print(f"    result: {json.dumps(err)}")
    except Exception:
        print(f"    result: {raw[:200]!r}")
    return False


# ---------------------------------------------------------------------------
# Strategy 3 — /v1/chat/completions with modalities=["audio"]
# ---------------------------------------------------------------------------

def strategy_chat_audio(base: str, model: str, text: str, voice: str, out: str) -> bool:
    url  = base + "/v1/chat/completions"
    body = json.dumps({
        "model":      model,
        "modalities": ["audio"],
        "audio": {"voice": voice, "format": "wav"},
        "messages": [{"role": "user", "content": text}],
    }).encode()
    print(f"[3] POST {url}")
    status, raw = _post(url, body, "application/json")
    print(f"    status: {status}")
    if status != 200:
        try:
            print(f"    result: {json.dumps(json.loads(raw))}")
        except Exception:
            print(f"    result: {raw[:200]!r}")
        return False
    try:
        result  = json.loads(raw)
        message = result["choices"][0]["message"]
        node    = message.get("audio")
        if node and node.get("data"):
            audio = base64.b64decode(node["data"])
            print("    result: audio in message.audio.data")
            _save_and_report(audio, out)
            return True
        # No audio field — model responded with text
        content = message.get("content", "")
        print(f"    result: no audio field; text: {content[:120]!r}")
        return False
    except Exception as e:
        print(f"    result: parse error: {e}")
        print(f"    raw: {raw[:400].decode(errors='replace')}")
        return False


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main():
    ap = argparse.ArgumentParser(description="Test LFM2.5-Audio TTS (multi-strategy)")
    ap.add_argument("--url",      default=DEFAULT_URL,   help="llama.cpp router base URL")
    ap.add_argument("--model",    default=DEFAULT_MODEL, help="Model ID")
    ap.add_argument("--text",     default=DEFAULT_TEXT,  help="Text to synthesise")
    ap.add_argument("--voice",    default="default",     help="Voice name")
    ap.add_argument("--out",      default=DEFAULT_OUT,   help="Output audio file path")
    ap.add_argument("--strategy", type=int, choices=[1, 2, 3], default=None,
                    help="Force a specific strategy (default: try all in order)")
    args = ap.parse_args()

    base = args.url.rstrip("/")
    print(f"model : {args.model}")
    print(f"text  : {args.text!r}")
    print()

    strategies = [args.strategy] if args.strategy else [1, 2, 3]
    for s in strategies:
        if s == 1:
            ok = strategy_openai_speech(base, args.model, args.text, args.voice, args.out)
        elif s == 2:
            ok = strategy_native_tts(base, args.model, args.text, args.out)
        else:
            ok = strategy_chat_audio(base, args.model, args.text, args.voice, args.out)
        if ok:
            print(f"\nSuccess via strategy {s}.")
            return
        print()

    print("All strategies failed — no audio returned.")
    print("The router may not expose audio endpoints for this model.")
    print("The per-model server is bound to 127.0.0.1:38309 on the remote host,")
    print("so it cannot be reached directly from this machine.")


if __name__ == "__main__":
    main()
