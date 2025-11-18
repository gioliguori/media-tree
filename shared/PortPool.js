export class PortPool {
    constructor(base, size = 1000) {
        this.base = base;
        this.size = size;
        this.max = base + size;

        // sStack di porte disponibili
        this.available = [];

        // Set di porte allocate (per validazione)
        this.allocated = new Set();

        // Prossima porta mai usata
        this.nextUnused = base;

        // Stats
        this.stats = {
            totalAllocations: 0,
            totalReleases: 0,
            reused: 0,
            fresh: 0
        };
    }

    //Alloca una coppia di porte (audio, video)
    allocate() {
        // Prova a riutilizzare porte rilasciate
        if (this.available.length >= 2) {
            const videoPort = this.available.pop();
            const audioPort = this.available.pop();

            this.allocated.add(audioPort);
            this.allocated.add(videoPort);

            this.stats.totalAllocations++;
            this.stats.reused++;

            return { audioPort, videoPort };
        }

        // Alloca nuove porte dal range mai usato
        if (this.nextUnused + 2 > this.max) {
            throw new Error(
                `Port pool exhausted: base=${this.base}, max=${this.max}, ` +
                `allocated=${this.allocated.size}, available=${this.available.length}`
            );
        }

        const audioPort = this.nextUnused;
        const videoPort = this.nextUnused + 1;

        this.allocated.add(audioPort);
        this.allocated.add(videoPort);

        this.nextUnused += 2;
        this.stats.totalAllocations++;
        this.stats.fresh++;

        return { audioPort, videoPort };
    }

    // Rilascia una coppia di porte per riutilizzo
    release(audioPort, videoPort) {
        // Validazione
        if (!this.allocated.has(audioPort) || !this.allocated.has(videoPort)) {
            console.warn(`[PortPool] Attempted to release unallocated ports: ${audioPort}, ${videoPort}`);
            return;
        }

        // Rimuovi da allocated
        this.allocated.delete(audioPort);
        this.allocated.delete(videoPort);

        // Aggiungi al pool disponibili (LIFO)
        this.available.push(audioPort);
        this.available.push(videoPort);

        this.stats.totalReleases++;
    }
    // Marca porte come allocate durante recovery
    markAsAllocated(audioPort, videoPort) {
        // Validazione range
        if (audioPort < this.base || audioPort >= this.max) {
            throw new Error(`Audio port ${audioPort} out of range`);
        }
        if (videoPort < this.base || videoPort >= this.max) {
            throw new Error(`Video port ${videoPort} out of range`);
        }

        // Se già allocate, skip
        if (this.allocated.has(audioPort) && this.allocated.has(videoPort)) {
            console.warn(`[PortPool] Ports  already allocated`);
            return;
        }

        // Rimuovi da available
        this.available = this.available.filter(p => p !== audioPort && p !== videoPort);

        // Marca come allocate
        this.allocated.add(audioPort);
        this.allocated.add(videoPort);

        // Aggiorna nextUnused
        const maxPort = Math.max(audioPort, videoPort);
        if (maxPort >= this.nextUnused) {
            this.nextUnused = maxPort + 1;
        }

        // Statistiche
        this.stats.totalAllocations++;
    }

    // Verifica se una porta è allocata
    isAllocated(port) {
        return this.allocated.has(port);
    }

    // Ottieni statistiche del pool
    getStats() {
        return {
            ...this.stats,
            allocated: this.allocated.size,
            available: this.available.length,
            capacity: this.size,
            utilizationPercent: (this.allocated.size / this.size * 100).toFixed(2),
            nextUnused: this.nextUnused,
        };
    }

    // Reset completo
    reset() {
        this.available = [];
        this.allocated.clear();
        this.nextUnused = this.base;
        this.stats = {
            totalAllocations: 0,
            totalReleases: 0,
            reused: 0,
            fresh: 0
        };
    }
}