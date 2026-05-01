#!/usr/bin/env python3
"""
test-audio-ping.py — verify the LFM2.5-Audio backend responds to plain text
before attempting audio inference.

Sends a minimal /v1/chat/completions request (no audio) to the model and
measures round-trip time.  If this hangs or 404s, the model backend itself
is not reachable through the router.

Usage:
    python3 scripts/test-audio-ping.py [--url URL] [--model MODEL]
"""

import argparse
import json
import ssl
import sys
import time
import urllib.error
import urllib.request

DEFAULT_URL   = "https://192.168.1.152:8080"
DEFAULT_MODEL = "lfm2.5_audio-1.5b_q8-0"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--url",     default=DEFAULT_URL)
    ap.add_argument("--model",   default=DEFAULT_MODEL)
    ap.add_argument("--timeout", type=int, default=30, help="Request timeout in seconds")
    args = ap.parse_args()

    ctx = ssl.create_default_context()
    ctx.check_hostname = False
    ctx.verify_mode    = ssl.CERT_NONE

    endpoint = args.url.rstrip("/") + "/v1/chat/completions"
    payload  = json.dumps({
        "model":      args.model,
        "max_tokens": 5,
        "messages":   [{"role": "user", "content": "Reply with one word: OK"}],
    }).encode()

    req = urllib.request.Request(
        endpoint, data=payload,
        headers={"Content-Type": "application/json"},
        method="POST",
    )

    print(f"POST {endpoint}")
    print(f"  model   : {args.model}")
    print(f"  timeout : {args.timeout}s")

    t0 = time.monotonic()
    try:
        with urllib.request.urlopen(req, context=ctx, timeout=args.timeout) as resp:
            elapsed = time.monotonic() - t0
            raw     = resp.read()
    except TimeoutError:
        print(f"\nTIMEOUT after {args.timeout}s — model backend is not responding")
        sys.exit(1)
    except urllib.error.HTTPError as e:
        raw = e.read()
        print(f"\nERROR {e.code}: {e.reason}")
        try:
            print(json.dumps(json.loads(raw), indent=2))
        except Exception:
            print(raw.decode(errors="replace"))
        sys.exit(1)

    try:
        result  = json.loads(raw)
        content = result["choices"][0]["message"]["content"]
        print(f"\nOK  ({elapsed:.2f}s)  →  {content!r}")
    except Exception:
        print(f"\nResponse ({elapsed:.2f}s):")
        print(raw.decode(errors="replace"))


if __name__ == "__main__":
    main()
