// Load tests for the live-score API (not the upstream provider).
//
// Start a mock provider and the API against it first:
//   go run ./cmd/mockprovider -addr :9090 -delay 100ms &
//   SPORTSCORE_BASE_URL=http://localhost:9090 go run ./cmd/api &
//
// Then pick a scenario:
//   k6 run -e SCENARIO=warm tests/k6/matches.js
//
// Scenarios (see docs in README "Load testing"):
//   warm       steady reads, cache warmed first
//   coldburst  many VUs hit the same cold key at once (exercises singleflight)
//   hotkey     one popular key, high arrival rate
//   manykeys   many distinct limit/sport keys (cache churn)
//   spike      sustained load followed by a spike
//   (Redis-down / slow-provider runs: stop Redis, or restart mockprovider with -delay/-fail,
//    then run "warm" or "manykeys" and compare.)
//
// Record machine specs, cache state, duration and concurrency with each result.
import http from 'k6/http';
import { check } from 'k6';
import { Counter, Rate } from 'k6/metrics';

const BASE = __ENV.BASE_URL || 'http://localhost:8080';
const SCENARIO = __ENV.SCENARIO || 'warm';

const hits = new Counter('cache_hit');
const misses = new Counter('cache_miss');
const bypass = new Counter('cache_bypass');
const shed = new Rate('load_shed_503');

const scenarios = {
  warm: {
    executor: 'constant-arrival-rate', rate: 500, timeUnit: '1s', duration: '30s',
    preAllocatedVUs: 50, maxVUs: 200, exec: 'hot',
  },
  coldburst: {
    executor: 'per-vu-iterations', vus: 100, iterations: 1, maxDuration: '30s', exec: 'cold',
  },
  hotkey: {
    executor: 'constant-arrival-rate', rate: 1000, timeUnit: '1s', duration: '30s',
    preAllocatedVUs: 100, maxVUs: 400, exec: 'hot',
  },
  manykeys: {
    executor: 'constant-arrival-rate', rate: 300, timeUnit: '1s', duration: '30s',
    preAllocatedVUs: 50, maxVUs: 300, exec: 'varied',
  },
  spike: {
    executor: 'ramping-arrival-rate', startRate: 100, timeUnit: '1s',
    preAllocatedVUs: 100, maxVUs: 600, exec: 'hot',
    stages: [
      { target: 200, duration: '30s' }, { target: 200, duration: '1m' },
      { target: 1500, duration: '10s' }, { target: 1500, duration: '30s' },
      { target: 200, duration: '10s' }, { target: 200, duration: '30s' },
    ],
  },
};

export const options = {
  scenarios: { [SCENARIO]: scenarios[SCENARIO] },
  // Thresholds are placeholders: replace with numbers derived from your own baseline run.
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<250'],
  },
};

export function setup() {
  // Warm the cache for scenarios that expect it.
  if (SCENARIO !== 'coldburst') http.get(`${BASE}/api/v1/matches?sport=football&limit=5`);
  // One cold key shared by every VU in the burst.
  return { coldLimit: 6 + (Date.now() % 90) };
}

function record(res) {
  const c = res.headers['X-Cache'];
  if (c === 'HIT') hits.add(1); else if (c === 'MISS') misses.add(1); else if (c === 'BYPASS') bypass.add(1);
  shed.add(res.status === 503);
  check(res, { 'status is 200': (r) => r.status === 200 });
}

export function hot() {
  record(http.get(`${BASE}/api/v1/matches?sport=football&limit=5`));
}

export function cold(data) {
  record(http.get(`${BASE}/api/v1/matches?sport=football&limit=${data.coldLimit}`));
}

const sports = ['football', 'basketball', 'tennis', 'cricket'];
export function varied() {
  const sport = sports[Math.floor(Math.random() * sports.length)];
  const limit = 1 + Math.floor(Math.random() * 100);
  record(http.get(`${BASE}/api/v1/matches?sport=${sport}&limit=${limit}`));
}
