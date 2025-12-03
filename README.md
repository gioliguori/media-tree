# media-tree
PROBLEMI 
- Egress node (C)  

- Generali
    - Non gestiamo le azioni di creazione/distruzione tramite transazioni
    - Mancano stats varie

COMANDI UTILI GO

    # Esegui programma
go run cmd/controller/main.go

# Build eseguibile
go build -o controller cmd/controller/main.go
./controller

# Install dipendenze
go mod download

# Tidy dipendenze (rimuove unused)
go mod tidy

# Test
go test ./...

# Format codice
go fmt ./...

# Vet (controllo errori comuni)
go vet ./...

---

# STRUTTURA FINALE
controller/
├── go.mod                          # Dipendenze
├── go.sum                          # Lock file
├── Makefile                        # Comandi build
├── README.md                       # Documentazione
│
├── cmd/
│   └── controller/
│       └── main.go                 # Entry point
│
├── internal/                       # Codice privato
│   ├── config/
│   │   ├── config.go              # Strutture config
│   │   └── loader.go              # Carica YAML
│   │
│   ├── redis/
│   │   ├── client.go              # Wrapper Redis
│   │   └── pubsub.go              # Pub/Sub helpers
│   │
│   ├── tree/
│   │   ├── manager.go             # TreeManager
│   │   └── topology.go            # Operazioni topologia
│   │
│   ├── session/
│   │   ├── manager.go             # SessionManager
│   │   ├── ssrc.go                # Allocazione SSRC
│   │   └── routing.go             # Decisioni routing
│   │
│   ├── provisioner/
│   │   ├── provisioner.go         # Interface
│   │   ├── docker.go              # Implementazione Docker
│   │   └── mock.go                # Mock per test
│   │
│   └── api/
│       ├── server.go              # HTTP server
│       └── handlers.go            # Endpoints
│
├── pkg/                           # Codice pubblico
│   └── types/
│       └── types.go               # Tipi condivisi
│
└── config/
    └── default.yaml               # Config default