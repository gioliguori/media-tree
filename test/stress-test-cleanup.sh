#!/bin/bash

# Script di cleanup per stress test

set -e

CONTROLLER_URL="http://localhost:8080"
AUTO_CONFIRM=${1:-""}

echo "=================================================="
echo "STRESS TEST CLEANUP"
echo "=================================================="
echo ""

# Conta container audio
AUDIO_RUNNING=$(docker ps --filter 'name=whip-stress-*' -q 2>/dev/null | wc -l | tr -d ' ')
AUDIO_ALL=$(docker ps -a --filter 'name=whip-stress-*' -q 2>/dev/null | wc -l | tr -d ' ')

# Conta container video
VIDEO_RUNNING=$(docker ps --filter 'name=whip-stress-*' -q 2>/dev/null | wc -l | tr -d ' ')
VIDEO_ALL=$(docker ps -a --filter 'name=whip-stress-*' -q 2>/dev/null | wc -l | tr -d ' ')

TOTAL=$((AUDIO_ALL + VIDEO_ALL))

echo "Container trovati:"
echo "  - Audio: $AUDIO_ALL ($AUDIO_RUNNING running)"
echo "  - Video: $VIDEO_ALL ($VIDEO_RUNNING running)"
echo "  - Totale: $TOTAL"

if [ "$TOTAL" -eq 0 ]; then
    echo "Nessun container da rimuovere."
else
    # Stop AUDIO
    if [ "$AUDIO_RUNNING" -gt 0 ]; then
        echo ""
        echo "--- STOP GRACEFUL AUDIO CLIENTS ---"
        docker stop -t 10 $(docker ps --filter 'name=whip-stress-*' -q) 2>/dev/null || true
    fi
    
    # Stop VIDEO
    if [ "$VIDEO_RUNNING" -gt 0 ]; then
        echo ""
        echo "--- STOP GRACEFUL VIDEO CLIENTS ---"
        docker stop -t 10 $(docker ps --filter 'name=whip-stress-video-*' -q) 2>/dev/null || true
    fi
    
    if [ "$TOTAL" -gt 0 ]; then
        echo "Attendo 2s per elaborazione Janus..."
        sleep 2
        
        echo ""
        echo "--- RIMOZIONE CONTAINER ---"
        docker rm $(docker ps -a --filter 'name=whip-stress-*' -q) 2>/dev/null || true
        docker rm $(docker ps -a --filter 'name=whip-stress-video-*' -q) 2>/dev/null || true
        echo " $TOTAL container rimossi"
    fi
fi

echo ""

if [ "$AUTO_CONFIRM" != "auto" ]; then
    read -p "Vuoi eliminare anche le sessioni dal controller? (y/N): " -n 1 -r
    echo ""
else
    REPLY="y"
    echo "Modalità automatica:  eliminazione sessioni..."
fi

if [[ $REPLY =~ ^[Yy]$ ]]; then
    echo ""
    echo "--- ELIMINAZIONE SESSIONI DAL CONTROLLER ---"
    
    # Sessioni audio
    SESSIONS_AUDIO=$(curl -s "$CONTROLLER_URL/api/sessions" 2>/dev/null | jq -r '.[] | select(.sessionId | startswith("stress-test-")) | .sessionId' 2>/dev/null || echo "")
    
    # Sessioni video
    SESSIONS_VIDEO=$(curl -s "$CONTROLLER_URL/api/sessions" 2>/dev/null | jq -r '.[] | select(.sessionId | startswith("stress-test-video-")) | .sessionId' 2>/dev/null || echo "")
    
    ALL_SESSIONS=$(echo -e "$SESSIONS_AUDIO\n$SESSIONS_VIDEO" | grep -v '^$')
    SESSION_COUNT=$(echo "$ALL_SESSIONS" | wc -l | tr -d ' ')
    
    if [ "$SESSION_COUNT" -eq 0 ] || [ -z "$ALL_SESSIONS" ]; then
        echo "Nessuna sessione stress-test-* trovata."
    else
        echo "Sessioni da eliminare: $SESSION_COUNT"
        
        for SESSION_ID in $ALL_SESSIONS; do
            echo "  └─ Eliminazione: $SESSION_ID"
            curl -s -X DELETE "$CONTROLLER_URL/api/sessions/$SESSION_ID" > /dev/null 2>&1
        done
        
        echo " $SESSION_COUNT sessioni eliminate"
    fi
fi

echo ""
echo "=================================================="
echo "CLEANUP COMPLETATO"
echo "=================================================="
echo ""