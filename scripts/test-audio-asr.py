#!/usr/bin/env python3
"""
test-audio-asr.py — smoke test for LFM2.5-Audio ASR via llama.cpp router.

Hits POST /v1/audio/transcriptions as multipart/form-data.  This endpoint
is exposed by the router since llama.cpp PR #22000.

Usage:
    python3 scripts/test-audio-asr.py [--url URL] --file AUDIO_FILE
    python3 scripts/test-audio-asr.py [--url URL] --generate-tone

    --file AUDIO_FILE   WAV/MP3/OGG/FLAC to transcribe.
    --generate-tone     Generate a 1 s 440 Hz sine-wave WAV as a connectivity
                        test (transcription will be nonsense — that's fine).

Defaults:
    url   https://192.168.1.152:8080
    model lfm2.5_audio-1.5b_q8-0
"""

import argparse
import io
import json
import math
import os
import ssl
import struct
import sys
import urllib.error
import urllib.request

DEFAULT_URL   = "https://192.168.1.152:8080"
DEFAULT_MODEL = "lfm2.5_audio-1.5b_q8-0"


def _make_tone_wav(freq_hz: float = 440.0, duration_s: float = 1.0,
                   sample_rate: int = 16000) -> bytes:
    n = int(sample_rate * duration_s)
    pcm = struct.pack(f"<{n}h",
                      *[int(32767 * math.sin(2 * math.pi * freq_hz * i / sample_rate))
                        for i in range(n)])
    buf = io.BytesIO()
    buf.write(b"RIFF")
    buf.write(struct.pack("<I", 36 + len(pcm)))
    buf.write(b"WAVE")
    buf.write(b"fmt ")
    buf.write(struct.pack("<IHHIIHH", 16, 1, 1, sample_rate, sample_rate * 2, 2, 16))
    buf.write(b"data")
    buf.write(struct.pack("<I", len(pcm)))
    buf.write(pcm)
    return buf.getvalue()


def _encode_multipart(fields: dict, files: dict) -> tuple[bytes, str]:
    boundary = b"----StoaAudioBoundary"
    parts = []
    for name, value in fields.items():
        parts.append(
            b"--" + boundary + b"\r\n"
            + f'Content-Disposition: form-data; name="{name}"\r\n\r\n'.encode()
            + str(value).encode() + b"\r\n"
        )
    for name, (filename, content_type, data) in files.items():
        parts.append(
            b"--" + boundary + b"\r\n"
            + f'Content-Disposition: form-data; name="{name}"; filename="{filename}"\r\n'.encode()
            + f"Content-Type: {content_type}\r\n\r\n".encode()
            + data + b"\r\n"
        )
    parts.append(b"--" + boundary + b"--\r\n")
    return b"".join(parts), "multipart/form-data; boundary=" + boundary.decode()


def _ssl_ctx() -> ssl.SSLContext:
    ctx = ssl.create_default_context()
    ctx.check_hostname = False
    ctx.verify_mode    = ssl.CERT_NONE
    return ctx


def main():
    ap = argparse.ArgumentParser(description="Test LFM2.5-Audio ASR via /v1/audio/transcriptions")
    ap.add_argument("--url",           default=DEFAULT_URL,   help="llama.cpp router base URL")
    ap.add_argument("--model",         default=DEFAULT_MODEL, help="Model ID")
    ap.add_argument("--file",          default=None,          help="Audio file to transcribe")
    ap.add_argument("--generate-tone", action="store_true",   help="Generate a test tone instead")
    ap.add_argument("--language",      default=None,          help="Language hint (e.g. 'en')")
    ap.add_argument("--timeout",       type=int, default=120, help="Request timeout in seconds (default 120)")
    args = ap.parse_args()

    if not args.file and not args.generate_tone:
        ap.error("Provide --file AUDIO_FILE or use --generate-tone")

    if args.generate_tone:
        audio_bytes  = _make_tone_wav()
        filename     = "test-tone.wav"
        content_type = "audio/wav"
        print("Generated 1 s 440 Hz sine-wave WAV (16 kHz mono, 16-bit PCM)")
    else:
        with open(args.file, "rb") as f:
            audio_bytes = f.read()
        filename     = os.path.basename(args.file)
        ext          = os.path.splitext(filename)[1].lower()
        content_type = {
            ".wav": "audio/wav", ".mp3": "audio/mpeg", ".ogg": "audio/ogg",
            ".flac": "audio/flac", ".m4a": "audio/mp4",
        }.get(ext, "application/octet-stream")

    fields = {"model": args.model}
    if args.language:
        fields["language"] = args.language

    body, ct = _encode_multipart(
        fields=fields,
        files={"file": (filename, content_type, audio_bytes)},
    )

    endpoint = args.url.rstrip("/") + "/v1/audio/transcriptions"
    req = urllib.request.Request(
        endpoint, data=body,
        headers={"Content-Type": ct},
        method="POST",
    )

    print(f"POST {endpoint}")
    print(f"  model : {args.model}")
    print(f"  file  : {filename}  ({len(audio_bytes)} bytes)")

    try:
        with urllib.request.urlopen(req, context=_ssl_ctx(), timeout=args.timeout) as resp:
            status = resp.status
            raw    = resp.read()
    except TimeoutError:
        print(f"\nTIMEOUT after {args.timeout}s")
        sys.exit(1)
    except urllib.error.HTTPError as e:
        raw = e.read()
        print(f"\nERROR {e.code}: {e.reason}")
        try:
            print(json.dumps(json.loads(raw), indent=2))
        except Exception:
            print(raw.decode(errors="replace"))
        sys.exit(1)

    print(f"\nStatus : {status}")
    try:
        result = json.loads(raw)
        print(f"Text   : {result.get('text', '(no text field)')}")
        if result.get("usage"):
            u = result["usage"]
            print(f"Tokens : in={u.get('input_tokens')}  out={u.get('output_tokens')}")
    except Exception:
        print("Raw response:")
        print(raw.decode(errors="replace"))


if __name__ == "__main__":
    main()
