import redis
import json

# Configura la connessione
r = redis.Redis(host='localhost', port=6379, db=0, decode_responses=True)

data = {}

# Scansiona tutte le chiavi
for key in r.scan_iter("*"):
    key_type = r.type(key)
    
    if key_type == 'string':
        data[key] = r.get(key)
    elif key_type == 'hash':
        data[key] = r.hgetall(key)
    elif key_type == 'list':
        data[key] = r.lrange(key, 0, -1)
    elif key_type == 'set':
        data[key] = list(r.smembers(key))
    elif key_type == 'zset':
        data[key] = r.zrange(key, 0, -1, withscores=True)

# Salva su file
with open('backup_redis.json', 'w') as f:
    json.dump(data, f, indent=2)

print("Esportazione completata in backup_redis.json")