##!/usr/bin/env bash
#set -euo pipefail
#
#speaches serve &
#PID=$!
#
#echo "Esperando a que el API esté listo..."
#for i in $(seq 1 60); do
#  if curl -sf http://127.0.0.1:8000/health >/dev/null 2>&1; then
#    echo "API OK"
#    break
#  fi
#  sleep 2
#done
#
#echo "Warming up Systran/faster-whisper-small..."
#curl -sS -X POST http://127.0.0.1:8000/v1/models/Systran/faster-whisper-small || true
#echo "Warmup finalizado."
#
#wait $PID