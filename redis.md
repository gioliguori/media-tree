┌─────────────────────┬──────────────────────────────────────────────┐
│ CHI                 │ COSA SCRIVE SU REDIS                         │
├─────────────────────┼──────────────────────────────────────────────┤
│ Controller          │ tree:{treeId}:parent:{nodeId}                │
│                     │ tree:{treeId}:children:{nodeId}              │
│                     │ (topologia per tree)                         │
├─────────────────────┼──────────────────────────────────────────────┤
│ BaseNode (tutti)    │ tree:{treeId}:node:{nodeId}                  │
│                     │ tree:{treeId}:{nodeType}                     │
│                     │ (self-registration per tree)                 │
├─────────────────────┼──────────────────────────────────────────────┤
│ InjectionNode       │ tree:{treeId}:session:{sessionId}            │ 
│                     │ sessions:{treeId}                            │
│                     │ (session data + tree index)                  │
├─────────────────────┼──────────────────────────────────────────────┤
│ EgressNode          │ tree:{treeId}:mountpoint:{nodeId}:{sessionId}│
│                     │ mountpoints:{treeId}                         │
│                     │ tree:{treeId}:mountpoints:node:{nodeId}      │
├─────────────────────┼──────────────────────────────────────────────┤
│ RelayNode           │ (nulla! solo forward)                        │
└─────────────────────┴──────────────────────────────────────────────┘

═══════════════════════════════════════════════════════════════════
REDIS KEYS - COMPLETE STRUCTURE (MULTI-TREE)
═══════════════════════════════════════════════════════════════════

1. TOPOLOGIA ALBERO (per tree)
   tree:{treeId}:parent:{nodeId}              STRING   → parentNodeId
   tree:{treeId}:children:{nodeId}            SET      → {childId1, childId2, ...}

2. REGISTRY NODI (per tree)
   tree:{treeId}:node:{nodeId}                HASH     → {nodeId, nodeType, treeId, host, 
                                                          port, audioPort, videoPort, 
                                                          status, created}
   tree:{treeId}:{nodeType}                   SET      → {nodeId1, nodeId2, ...}
                                                          (injection, relay, egress)

3. SESSIONI BROADCAST (INJECTION NODE, per tree)
   tree:{treeId}:session:{sessionId}          HASH     → {sessionId, treeId, roomId, 
                                                          audioSsrc, videoSsrc, recipients, 
                                                          injectionNodeId, active, 
                                                          createdAt, updatedAt}
   sessions:{treeId}                          SET      → {sessionId1, sessionId2, ...}

4. MOUNTPOINT (EGRESS NODES, per tree)
   tree:{treeId}:mountpoint:{nodeId}:{sessionId}  HASH → {sessionId, treeId, mountpointId, 
                                                          audioSsrc, videoSsrc, 
                                                          janusAudioPort, janusVideoPort, 
                                                          egressNodeId, active, 
                                                          createdAt, updatedAt}
   mountpoints:{treeId}                       SET      → {nodeId:sessionId, ...}
   tree:{treeId}:mountpoints:node:{nodeId}    SET      → {sessionId1, sessionId2, ...}

═══════════════════════════════════════════════════════════════════
PUB/SUB CHANNELS - COMPLETE STRUCTURE (MULTI-TREE)
═══════════════════════════════════════════════════════════════════

1. TOPOLOGY EVENTS (per tree e nodo)
   
   topology:{treeId}                          → Eventi globali albero
      - topology-reset                           (full sync richiesto)
   
   topology:{treeId}:{nodeId}                 → Eventi specifici nodo
      - parent-changed                           {type, nodeId, oldParent, newParent}
      - child-added                              {type, nodeId, childId}
      - child-removed                            {type, nodeId, childId}

2. SESSION EVENTS (per tree)
   
   sessions:{treeId}                          → Eventi globali sessioni tree
      - session-created                          {type, sessionId, treeId}
      - session-destroyed                        {type, sessionId, treeId}
   
   sessions:{treeId}:{nodeId}                 → Eventi specifici nodo
      (riservato per futuri eventi nodo-specific)

═══════════════════════════════════════════════════════════════════
SUBSCRIBERS (chi si iscrive a cosa)
═══════════════════════════════════════════════════════════════════

InjectionNode subscribe a:
   - topology:{treeId}:{nodeId}               (parent-changed, child-added, child-removed)
   - topology:{treeId}                        (topology-reset)
   - sessions:{treeId}:{nodeId}               (eventi sessioni nodo-specific)

RelayNode subscribe a:
   - topology:{treeId}:{nodeId}               (parent-changed, child-added, child-removed)
   - topology:{treeId}                        (topology-reset)

EgressNode subscribe a:
   - topology:{treeId}:{nodeId}               (parent-changed, child-added, child-removed)
   - topology:{treeId}                        (topology-reset)
   - sessions:{treeId}                        (session-created, session-destroyed)
   - sessions:{treeId}:{nodeId}               (eventi sessioni nodo-specific)

═══════════════════════════════════════════════════════════════════
ESEMPI DI EVENTI PUB/SUB
═══════════════════════════════════════════════════════════════════

# Cambio parent (da Controller)
PUBLISH topology:tree-1:egress-1 '{"type":"parent-changed","nodeId":"egress-1","newParent":"relay-1"}'

# Aggiunta figlio (da Controller)
PUBLISH topology:tree-1:relay-1 '{"type":"child-added","nodeId":"relay-1","childId":"egress-1"}'

# Rimozione figlio (da Controller)
PUBLISH topology:tree-1:relay-1 '{"type":"child-removed","nodeId":"relay-1","childId":"egress-1"}'

# Reset topologia globale (da Controller)
PUBLISH topology:tree-1 '{"type":"topology-reset"}'

# Sessione creata (da InjectionNode o Controller)
PUBLISH sessions:tree-1 '{"type":"session-created","sessionId":"broadcaster-1","treeId":"tree-1"}'

# Sessione distrutta (da InjectionNode o Controller)
PUBLISH sessions:tree-1 '{"type":"session-destroyed","sessionId":"broadcaster-1","treeId":"tree-1"}'

═══════════════════════════════════════════════════════════════════
TTL POLICY
═══════════════════════════════════════════════════════════════════

tree:{treeId}:node:{nodeId}                   TTL: 600s (10min, refreshato da heartbeat)
tree:{treeId}:session:{sessionId}             TTL: 86400s (24h, dopo deactivate)
tree:{treeId}:mountpoint:{nodeId}:{sessionId} TTL: 86400s (24h, dopo deactivate)

═══════════════════════════════════════════════════════════════════
NOTE IMPLEMENTATIVE
═══════════════════════════════════════════════════════════════════

1. ISOLAMENTO TREE
   - Ogni tree è completamente isolato a livello Redis
   - Impossibile conflitto tra nodeId in tree diversi
   - Facilita gestione multi-tenant

2. PUB/SUB PATTERN
   - Canali specifici {treeId}:{nodeId} per eventi mirati
   - Canali globali {treeId} per broadcast a tutto il tree
   - Nessun filtraggio applicativo necessario

3. RECOVERY
   - Nodi leggono da Redis al boot per recovery state
   - TTL mantiene Redis pulito da nodi morti
   - Session/Mountpoint inactive marcati ma mantenuti 24h per debug

4. PERFORMANCE
   - HSET/HGET per atomic operations su nodi/sessioni/mountpoint
   - SET per indexes (fast membership check)
   - Pub/Sub non usa polling (event-driven)