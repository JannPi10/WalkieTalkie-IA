#!/bin/bash
set -euo pipefail

echo "Verificando disponibilidad de modelos..."
echo "Verificando modelo llama3.2:3b..."
max_attempts=30
attempt=0

while [ $attempt -lt $max_attempts ]; do
    if curl -sf http://deepseek:11434/api/tags | grep -q "llama3.2:3b"; then
        echo "Modelo llama3.2:3b disponible"
        break
    fi

    attempt=$((attempt + 1))
    echo "Intento $attempt/$max_attempts - Esperando modelo llama3.2:3b..."
    sleep 10
done

if [ $attempt -eq $max_attempts ]; then
    echo "Error: Modelo llama3.2:3b no disponible después de $max_attempts intentos"
    exit 1
fi

echo "Verificando servicio STT..."
attempt=0

while [ $attempt -lt $max_attempts ]; do
    if curl -sf http://stt:8000/health > /dev/null 2>&1; then
        echo "Servicio STT disponible"
        break
    fi

    attempt=$((attempt + 1))
    echo "Intento $attempt/$max_attempts - Esperando servicio STT..."
    sleep 5
done

if [ $attempt -eq $max_attempts ]; then
    echo "Error: Servicio STT no disponible después de $max_attempts intentos"
    exit 1
fi

echo "Todos los modelos están disponibles. Iniciando aplicación..."