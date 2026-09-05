package classifier

import (
	"strings"

	xrrredis "hop.top/xrr/adapters/redis"
)

// redisClass is the static command-name → Class table covering the
// Redis 7.x command surface. Subcommand-aware entries (CLUSTER RESET,
// MEMORY PURGE, CLIENT KILL, etc.) are handled in ClassifyRedisCmd.
//
// Source: redis.io/commands as of 2026-05. Refresh annually; cite
// the source version in this comment when bumping.
var redisClass = map[string]Class{
	// ── Read ────────────────────────────────────────────────────
	"GET": ClassRead, "MGET": ClassRead, "GETRANGE": ClassRead,
	"EXISTS": ClassRead, "KEYS": ClassRead, "SCAN": ClassRead,
	"TYPE": ClassRead, "STRLEN": ClassRead, "OBJECT": ClassRead,
	"DUMP": ClassRead, "TTL": ClassRead, "PTTL": ClassRead,
	"EXPIRETIME": ClassRead, "PEXPIRETIME": ClassRead,
	"RANDOMKEY": ClassRead, "TOUCH": ClassRead,
	"HGET": ClassRead, "HMGET": ClassRead, "HGETALL": ClassRead,
	"HKEYS": ClassRead, "HVALS": ClassRead, "HLEN": ClassRead,
	"HSCAN": ClassRead, "HEXISTS": ClassRead, "HSTRLEN": ClassRead,
	"HRANDFIELD": ClassRead,
	"LRANGE":     ClassRead, "LLEN": ClassRead, "LINDEX": ClassRead,
	"LPOS":     ClassRead,
	"SMEMBERS": ClassRead, "SCARD": ClassRead, "SISMEMBER": ClassRead,
	"SMISMEMBER": ClassRead, "SUNION": ClassRead, "SINTER": ClassRead,
	"SINTERCARD": ClassRead, "SDIFF": ClassRead, "SSCAN": ClassRead,
	"SRANDMEMBER": ClassRead,
	"ZRANGE":      ClassRead, "ZRANGEBYSCORE": ClassRead,
	"ZRANGEBYLEX": ClassRead, "ZREVRANGE": ClassRead,
	"ZREVRANGEBYSCORE": ClassRead, "ZREVRANGEBYLEX": ClassRead,
	"ZRANK": ClassRead, "ZREVRANK": ClassRead, "ZSCORE": ClassRead,
	"ZMSCORE": ClassRead, "ZCARD": ClassRead, "ZCOUNT": ClassRead,
	"ZLEXCOUNT": ClassRead, "ZSCAN": ClassRead, "ZRANDMEMBER": ClassRead,
	"PFCOUNT":  ClassRead,
	"BITCOUNT": ClassRead, "BITPOS": ClassRead, "GETBIT": ClassRead,
	"BITFIELD_RO": ClassRead,
	"GEOSEARCH":   ClassRead, "GEODIST": ClassRead, "GEOPOS": ClassRead,
	"GEOHASH": ClassRead,
	"XRANGE":  ClassRead, "XREVRANGE": ClassRead, "XLEN": ClassRead,
	"XREAD": ClassRead, "XINFO": ClassRead, "XPENDING": ClassRead,
	"INFO": ClassRead, "PING": ClassRead, "ECHO": ClassRead,
	"DBSIZE": ClassRead, "TIME": ClassRead, "LASTSAVE": ClassRead,
	"ROLE": ClassRead, "COMMAND": ClassRead, "MEMORY": ClassRead,
	"LATENCY": ClassRead, "WAIT": ClassRead, "LOLWUT": ClassRead,
	"AUTH": ClassRead, "HELLO": ClassRead, "SELECT": ClassRead,
	"SUBSCRIBE": ClassRead, "UNSUBSCRIBE": ClassRead,
	"PSUBSCRIBE": ClassRead, "PUNSUBSCRIBE": ClassRead,
	"PUBSUB": ClassRead,

	// ── Write ───────────────────────────────────────────────────
	"SET": ClassWrite, "MSET": ClassWrite, "GETSET": ClassWrite,
	"APPEND": ClassWrite, "INCR": ClassWrite, "DECR": ClassWrite,
	"INCRBY": ClassWrite, "DECRBY": ClassWrite, "INCRBYFLOAT": ClassWrite,
	"SETEX": ClassWrite, "PSETEX": ClassWrite, "SETNX": ClassWrite,
	"MSETNX": ClassWrite, "GETEX": ClassWrite, "SETRANGE": ClassWrite,
	"SUBSTR": ClassRead,
	"HSET":   ClassWrite, "HMSET": ClassWrite, "HSETNX": ClassWrite,
	"HINCRBY": ClassWrite, "HINCRBYFLOAT": ClassWrite,
	"LPUSH": ClassWrite, "RPUSH": ClassWrite, "LPUSHX": ClassWrite,
	"RPUSHX": ClassWrite, "LSET": ClassWrite, "LINSERT": ClassWrite,
	"LPOP": ClassWrite, "RPOP": ClassWrite, "BLPOP": ClassWrite,
	"BRPOP": ClassWrite, "LMPOP": ClassWrite, "BLMPOP": ClassWrite,
	"LMOVE": ClassWrite, "BLMOVE": ClassWrite, "RPOPLPUSH": ClassWrite,
	"BRPOPLPUSH": ClassWrite, "LREM": ClassWrite, "LTRIM": ClassWrite,
	"SADD": ClassWrite, "SMOVE": ClassWrite,
	"SUNIONSTORE": ClassWrite, "SINTERSTORE": ClassWrite,
	"SDIFFSTORE": ClassWrite,
	"ZADD":       ClassWrite, "ZINCRBY": ClassWrite,
	"ZRANGESTORE": ClassWrite, "ZUNIONSTORE": ClassWrite,
	"ZINTERSTORE": ClassWrite, "ZDIFFSTORE": ClassWrite,
	"ZUNION": ClassWrite, "ZINTER": ClassWrite, "ZDIFF": ClassWrite,
	"PFADD": ClassWrite, "PFMERGE": ClassWrite,
	"BITOP": ClassWrite, "SETBIT": ClassWrite, "BITFIELD": ClassWrite,
	"GEOADD": ClassWrite, "GEOSEARCHSTORE": ClassWrite,
	"EXPIRE": ClassWrite, "EXPIREAT": ClassWrite,
	"PEXPIRE": ClassWrite, "PEXPIREAT": ClassWrite, "PERSIST": ClassWrite,
	"RENAME": ClassWrite, "RENAMENX": ClassWrite,
	"COPY": ClassWrite, "RESTORE": ClassWrite, "MOVE": ClassWrite,
	"PUBLISH": ClassWrite, "SPUBLISH": ClassWrite,
	"XADD": ClassWrite, "XTRIM": ClassWrite, "XACK": ClassWrite,
	"XCLAIM": ClassWrite, "XAUTOCLAIM": ClassWrite, "XGROUP": ClassWrite,
	"XREADGROUP": ClassWrite, "XSETID": ClassWrite,
	"SAVE": ClassWrite, "BGSAVE": ClassWrite, "BGREWRITEAOF": ClassWrite,
	"CONFIG": ClassWrite, "CLIENT": ClassWrite, "SLOWLOG": ClassWrite,
	"REPLICAOF": ClassWrite, "SLAVEOF": ClassWrite,
	"EVAL": ClassWrite, "EVALSHA": ClassWrite, "FCALL": ClassWrite,
	"SCRIPT": ClassWrite, "FUNCTION": ClassWrite,

	// ── Destructive ─────────────────────────────────────────────
	"DEL": ClassDestructive, "UNLINK": ClassDestructive,
	"FLUSHDB": ClassDestructive, "FLUSHALL": ClassDestructive,
	"SREM": ClassDestructive, "HDEL": ClassDestructive,
	"ZREM":             ClassDestructive,
	"ZREMRANGEBYSCORE": ClassDestructive,
	"ZREMRANGEBYLEX":   ClassDestructive,
	"ZREMRANGEBYRANK":  ClassDestructive,
	"SPOP":             ClassDestructive, "ZPOPMIN": ClassDestructive,
	"ZPOPMAX":  ClassDestructive,
	"BZPOPMIN": ClassDestructive, "BZPOPMAX": ClassDestructive,
	"XDEL": ClassDestructive, "DEBUG": ClassDestructive,
	"SHUTDOWN": ClassDestructive, "SWAPDB": ClassDestructive,
}

// ClassifyRedis returns the Class for a Redis interaction. The
// command name is uppercased before lookup. Subcommand-aware
// commands (CLUSTER, MEMORY, CLIENT, SCRIPT, FUNCTION, DEBUG) are
// inspected for their first argument and rerouted to the right
// class when a subcommand signals destructive intent.
func ClassifyRedis(req *xrrredis.Request) Class {
	if req == nil {
		return ClassUnknown
	}
	return ClassifyRedisCmd(req.Command, req.Args)
}

// ClassifyRedisCmd is the underlying classifier; it is exported for
// adopter unit tests that exercise the table directly.
func ClassifyRedisCmd(cmd string, args []string) Class {
	if cmd == "" {
		return ClassUnknown
	}
	upper := strings.ToUpper(cmd)
	// Subcommand-aware reroutes. Each maps (cmd, subcmd) → Class
	// when the subcmd diverges from the parent command's default.
	if len(args) > 0 {
		sub := strings.ToUpper(args[0])
		switch upper {
		case "CLUSTER":
			switch sub {
			case "INFO", "NODES", "SLOTS", "COUNT-FAILURE-REPORTS",
				"COUNTKEYSINSLOT", "GETSLOTKEYS", "KEYSLOT",
				"MYID", "MYSHARDID", "SHARDS", "LINKS", "BUMPEPOCH":
				return ClassRead
			case "RESET", "FLUSHSLOTS":
				return ClassDestructive
			default:
				return ClassWrite
			}
		case "MEMORY":
			switch sub {
			case "USAGE", "STATS", "DOCTOR", "MALLOC-STATS":
				return ClassRead
			case "PURGE":
				return ClassDestructive
			default:
				return ClassWrite
			}
		case "CLIENT":
			switch sub {
			case "GETNAME", "ID", "INFO", "LIST", "NO-EVICT",
				"NO-TOUCH", "GETREDIR", "TRACKINGINFO":
				return ClassRead
			case "KILL":
				return ClassDestructive
			default:
				return ClassWrite
			}
		case "SCRIPT":
			switch sub {
			case "EXISTS":
				return ClassRead
			case "FLUSH":
				return ClassDestructive
			default:
				return ClassWrite
			}
		case "FUNCTION":
			switch sub {
			case "LIST", "DUMP", "STATS":
				return ClassRead
			case "FLUSH", "DELETE":
				return ClassDestructive
			default:
				return ClassWrite
			}
		case "DEBUG":
			switch sub {
			case "OBJECT", "SLEEP", "JMAP":
				return ClassRead
			case "RELOAD", "LOADAOF", "FLUSHALL":
				return ClassDestructive
			default:
				return ClassWrite
			}
		case "CONFIG":
			switch sub {
			case "GET":
				return ClassRead
			case "RESETSTAT":
				return ClassWrite
			case "SET", "REWRITE":
				return ClassWrite
			}
		case "COMMAND":
			return ClassRead
		}
	}
	if c, ok := redisClass[upper]; ok {
		return c
	}
	return ClassUnknown
}
