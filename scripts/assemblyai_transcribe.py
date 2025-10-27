#!/usr/bin/env python3
import json
import os
import sys
import tempfile

import assemblyai as aai


def main():
    api_key = os.environ.get("ASSEMBLYAI_API_KEY")
    if not api_key:
        print("missing ASSEMBLYAI_API_KEY", file=sys.stderr)
        sys.exit(2)

    audio = sys.stdin.buffer.read()
    if not audio:
        print("no audio received", file=sys.stderr)
        sys.exit(3)

    aai.settings.api_key = api_key
    config = aai.TranscriptionConfig(
        speech_model=aai.SpeechModel.universal,
        language_code="es",
    )

    with tempfile.NamedTemporaryFile(suffix=".wav", delete=False) as tmp:
        tmp.write(audio)
        tmp.flush()
        temp_path = tmp.name

    try:
        transcriber = aai.Transcriber(config=config)
        transcript = transcriber.transcribe(temp_path)
    finally:
        try:
            os.unlink(temp_path)
        except OSError:
            pass

    if transcript.status == aai.TranscriptStatus.error:
        print(transcript.error, file=sys.stderr)
        sys.exit(4)

    result = {"text": transcript.text or ""}
    sys.stdout.write(json.dumps(result))


if __name__ == "__main__":
    main()