#!/usr/bin/env bash
set -euo pipefail

# 1) Arranca el servidor en background
speaches serve &
PID=$!

# 2) Espera a que responda (hasta ~2 min)
echo "Esperando a que el API esté listo..."
for i in $(seq 1 60); do
  if curl -sf http://127.0.0.1:8000/health >/dev/null 2>&1; then
    echo "API OK"
    break
  fi
  sleep 2
done

# 3) Warmup del modelo (descarga/precache)
echo "Warming up Systran/faster-whisper-small..."
curl -sS -X POST http://127.0.0.1:8000/v1/models/Systran/faster-whisper-small || true
echo "Warmup finalizado."

# 4) Mantén el proceso principal en foreground
wait $PID

