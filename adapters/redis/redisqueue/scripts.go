package redisqueue

import "github.com/redis/go-redis/v9"

// supersededReply is what the fenced scripts return when the presented
// (runID, attempt) pair is no longer current; Go maps it to run.ErrSuperseded.
// It is distinctive: every success path returns a positive value.
const supersededReply = -1

// luaEnqueue makes runID claimable at readyAt unless it is already queued or
// leased (idempotent no-op — duplicate enqueues from janitors or
// resubmission are harmless).
//
//	KEYS[1] ready  KEYS[2] leased
//	ARGV[1] runID  ARGV[2] readyAt millis
//
// Returns 1 when added, 0 on the idempotent no-op.
const luaEnqueue = `
if redis.call('ZSCORE', KEYS[1], ARGV[1]) or redis.call('ZSCORE', KEYS[2], ARGV[1]) then
  return 0
end
redis.call('ZADD', KEYS[1], ARGV[2], ARGV[1])
return 1
`

// luaClaim hands the most overdue claimable run to the caller: first the
// lowest-score ready member due by now, else the lowest-score leased member
// whose lease has lapsed (expiry reclaim). The winner moves to leased with a
// fresh deadline and an incremented attempt — the increment on reclaim is
// what fences out the previous holder. A reclaim also increments the failure
// counter: the previous execution died holding the lease, which consumes
// retry budget; a fresh claim from ready only reads the counter.
//
//	KEYS[1] ready  KEYS[2] leased  KEYS[3] attempt  KEYS[4] failures
//	ARGV[1] now millis  ARGV[2] ttl millis
//
// Returns {runID, attempt, failures, deadline} or nil when nothing is
// claimable.
const luaClaim = `
local src = KEYS[1]
local cand = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', ARGV[1], 'LIMIT', 0, 1)
if #cand == 0 then
  src = KEYS[2]
  cand = redis.call('ZRANGEBYSCORE', KEYS[2], '-inf', ARGV[1], 'LIMIT', 0, 1)
end
if #cand == 0 then
  return false
end
local runID = cand[1]
redis.call('ZREM', src, runID)
local attempt = redis.call('HINCRBY', KEYS[3], runID, 1)
local failures
if src == KEYS[2] then
  failures = redis.call('HINCRBY', KEYS[4], runID, 1)
else
  failures = tonumber(redis.call('HGET', KEYS[4], runID)) or 0
end
local deadline = tonumber(ARGV[1]) + tonumber(ARGV[2])
redis.call('ZADD', KEYS[2], deadline, runID)
return {runID, attempt, failures, deadline}
`

// luaHeartbeat extends a lease iff it is still live and held by this
// attempt: attempt counter matches AND the run is in leased AND the lease
// deadline is still in the future. Anything else is a zombie.
//
//	KEYS[1] leased  KEYS[2] attempt
//	ARGV[1] runID  ARGV[2] attempt  ARGV[3] now millis  ARGV[4] ttl millis
//
// Returns the new deadline millis, or -1 (superseded).
const luaHeartbeat = `
local cur = redis.call('HGET', KEYS[2], ARGV[1])
if not cur or tonumber(cur) ~= tonumber(ARGV[2]) then
  return -1
end
local score = redis.call('ZSCORE', KEYS[1], ARGV[1])
if not score or tonumber(score) <= tonumber(ARGV[3]) then
  return -1
end
local deadline = tonumber(ARGV[3]) + tonumber(ARGV[4])
redis.call('ZADD', KEYS[1], deadline, ARGV[1])
return deadline
`

// luaRelease gives a run back voluntarily, claimable again at now+delay.
// Same fence as heartbeat: the caller must currently hold a live lease with
// a matching attempt. The failed flag says whether this execution consumed
// retry budget: 1 after a retryable error (failures increments), 0 for a
// mere interruption (graceful shutdown) — the increment happens only after
// the fence passes, so a zombie's release never burns budget.
//
//	KEYS[1] ready  KEYS[2] leased  KEYS[3] attempt  KEYS[4] failures
//	ARGV[1] runID  ARGV[2] attempt  ARGV[3] now millis  ARGV[4] delay millis
//	ARGV[5] failed flag (1 or 0)
//
// Returns 1, or -1 (superseded).
const luaRelease = `
local cur = redis.call('HGET', KEYS[3], ARGV[1])
if not cur or tonumber(cur) ~= tonumber(ARGV[2]) then
  return -1
end
local score = redis.call('ZSCORE', KEYS[2], ARGV[1])
if not score or tonumber(score) <= tonumber(ARGV[3]) then
  return -1
end
if tonumber(ARGV[5]) == 1 then
  redis.call('HINCRBY', KEYS[4], ARGV[1], 1)
end
redis.call('ZREM', KEYS[2], ARGV[1])
redis.call('ZADD', KEYS[1], tonumber(ARGV[3]) + tonumber(ARGV[4]), ARGV[1])
return 1
`

// luaSettle removes a run from delivery entirely. The fence is looser than
// heartbeat's on purpose: attempt must match and the run must still be in
// leased, but an expired-yet-unreclaimed lease may settle — the worker
// finished late but nobody else has claimed, so its result stands. BOTH
// hashes are deliberately kept: the attempt field so lineage survives
// settling, and the failures field so the retry budget survives a
// park/resume cycle (waiting is free, but it doesn't forgive past failures).
//
//	KEYS[1] ready  KEYS[2] leased  KEYS[3] attempt
//	ARGV[1] runID  ARGV[2] attempt
//
// Returns 1, or -1 (superseded).
const luaSettle = `
local cur = redis.call('HGET', KEYS[3], ARGV[1])
if not cur or tonumber(cur) ~= tonumber(ARGV[2]) then
  return -1
end
if not redis.call('ZSCORE', KEYS[2], ARGV[1]) then
  return -1
end
redis.call('ZREM', KEYS[2], ARGV[1])
redis.call('ZREM', KEYS[1], ARGV[1])
return 1
`

// luaAppend appends one delta to a run's stream, fenced like a heartbeat:
// a delta carrying an attempt below the run's current counter is a zombie.
// Appends at or above the current counter are allowed even when the run is
// not leased (an unknown run's counter is 0), matching the DeltaLog
// contract's advisory-flow semantics.
//
//	KEYS[1] attempt  KEYS[2] deltas:<runID>
//	ARGV[1] runID  ARGV[2] attempt  ARGV[3] record key  ARGV[4] kind  ARGV[5] payload
//
// Returns 1, or -1 (superseded).
const luaAppend = `
local cur = redis.call('HGET', KEYS[1], ARGV[1])
if cur and tonumber(ARGV[2]) < tonumber(cur) then
  return -1
end
redis.call('XADD', KEYS[2], '*', 'key', ARGV[3], 'attempt', ARGV[2], 'kind', ARGV[4], 'payload', ARGV[5])
return 1
`

// Script handles are shared package-wide; go-redis EVALSHAs with an EVAL
// fallback, so first use loads them.
var (
	scriptEnqueue   = redis.NewScript(luaEnqueue)
	scriptClaim     = redis.NewScript(luaClaim)
	scriptHeartbeat = redis.NewScript(luaHeartbeat)
	scriptRelease   = redis.NewScript(luaRelease)
	scriptSettle    = redis.NewScript(luaSettle)
	scriptAppend    = redis.NewScript(luaAppend)
)
