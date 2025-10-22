
┌─────────────────────┬──────────────────────────────────┐
│ CHI                 │ COSA SCRIVE SU REDIS             │
├─────────────────────┼──────────────────────────────────┤
│ Controller          │ parent:{nodeId}                  │
│                     │ children:{nodeId}                │
│                     │ (topologia)                      │
├─────────────────────┼──────────────────────────────────┤
│ BaseNode (tutti)    │ node:{nodeId}                    │
│                     │ nodes:{nodeType}                 │ non lo fa
│                     │ (self-registration)              │
├─────────────────────┼──────────────────────────────────┤
│ InjectionNode       │ session:{sessionId}              │ non lo fa 
│                     │ sessions:{nodeId}                │ non lo fa
│                     │ sessions:active                  │ non lo fa
│                     │ (session ownership)              │
├─────────────────────┼──────────────────────────────────┤
│ EgressNode          │ mountpoint:{egressId}:{mpId}     │ non lo fa
│                     │ mountpoints:{egressId}           │ non lo fa
│                     │ (mountpoint ownership)           │
├─────────────────────┼──────────────────────────────────┤
│ RelayNode           │ (nulla! solo forward)            │
└─────────────────────┴──────────────────────────────────┘

═══════════════════════════════════════════════════════════════════
REDIS KEYS - COMPLETE STRUCTURE
═══════════════════════════════════════════════════════════════════

1. TOPOLOGIA ALBERO
   parent:{nodeId}                    STRING   → parentNodeId
   children:{nodeId}                  SET      → {childId1, childId2}

2. REGISTRY NODI
   node:{nodeId}                      HASH     → {nodeType, host, port, ...}
   nodes:{nodeType}                   SET      → {nodeId1, nodeId2}

3. SESSIONI BROADCAST
   session:{sessionId}                HASH     → {sessionId, roomId, audioSsrc, videoSsrc, injectionNodeId, ...}
   sessions:{nodeId}                  SET      → {sessionId1, sessionId2}
   sessions:active                    SET      → {sessionId1, sessionId2}

4. MOUNTPOINT (EGRESS NODES)
   mountpoint:{egressNodeId}:{mountpointId}   HASH → {sessionId, mountpointId, audioSsrc, videoSsrc, ...}
   mountpoints:{egressNodeId}                 SET  → {mountpointId1, mountpointId2}