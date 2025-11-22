┌─────────────────────┬──────────────────────────────────────────────┐
│ CHI                 │ COSA SCRIVE SU REDIS                         │
├─────────────────────┼──────────────────────────────────────────────┤
│ Controller          │ parent:{nodeId}                              │
│                     │ children:{nodeId}                            │
│                     │ (topologia)                                  │
├─────────────────────┼──────────────────────────────────────────────┤
│ BaseNode (tutti)    │ node:{nodeId}                                │
│                     │ nodes:{nodeType}                             │
│                     │ (self-registration)                          │
├─────────────────────┼──────────────────────────────────────────────┤
│ InjectionNode       │ session:{sessionId}                          │ 
│                     │ sessions:{treeId}                            │
│                     │ (session data + tree index)                  │
├─────────────────────┼──────────────────────────────────────────────┤
│ EgressNode          │ mountpoint:{nodeId}:{sessionId}              │
│                     │ mountpoints:{treeId}                         │
│                     │ mountpoints:node:{nodeId}                    │
├─────────────────────┼──────────────────────────────────────────────┤
│ RelayNode           │ (nulla! solo forward)                        │
└─────────────────────┴──────────────────────────────────────────────┘

═══════════════════════════════════════════════════════════════════
REDIS KEYS - COMPLETE STRUCTURE
═══════════════════════════════════════════════════════════════════

1. TOPOLOGIA ALBERO
   parent:{nodeId}                    STRING   → parentNodeId
   children:{nodeId}                  SET      → {childId1, childId2, ...}

2. REGISTRY NODI
   node:{nodeId}                      HASH     → {nodeId, nodeType, treeId, host, port, 
                                                  audioPort, videoPort, status, createdAt}
   nodes:{nodeType}                   SET      → {nodeId1, nodeId2, ...}

3. SESSIONI BROADCAST (INJECTION NODE)
   session:{sessionId}                HASH     → {sessionId, treeId, roomId, audioSsrc, 
                                                  videoSsrc, recipients, injectionNodeId, 
                                                  active, createdAt, updatedAt}
   sessions:{treeId}                  SET      → {sessionId1, sessionId2, ...}

4. MOUNTPOINT (EGRESS NODES)
   mountpoint:{nodeId}:{sessionId}    HASH     → {sessionId, treeId, mountpointId, 
                                                  audioSsrc, videoSsrc, janusAudioPort, 
                                                  janusVideoPort, egressNodeId, active, 
                                                  createdAt, updatedAt}
   mountpoints:{treeId}               SET      → {nodeId:sessionId, ...}
   mountpoints:node:{nodeId}          SET      → {sessionId1, sessionId2, ...}