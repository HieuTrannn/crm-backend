// Load test for NFR-PERF-01: read APIs at up to 200 requests/second.
//
// Each virtual user (VU) logs in once with its own account (loadtest-NNN@acme.demo,
// created by `seed -users`), so no user exceeds the 100 requests/minute limit:
// 200 req/s spread over 200 users is about 60 requests/minute each.
// Run it through docker compose; see test/k6/README.md.
import http from 'k6/http';
import { check, fail, sleep } from 'k6';
import exec from 'k6/execution';

const BASE = __ENV.BASE_URL || 'http://localhost:8080';
const PASSWORD = __ENV.SEED_OWNER_PASSWORD;
const USERS = parseInt(__ENV.USERS || '200', 10);
const API = `${BASE}/api/v1`;
const JSON_HEADERS = { 'Content-Type': 'application/json' };

export const options = {
  // Keep each VU's session cookies across iterations; k6 clears them by default.
  noCookiesReset: true,
  scenarios: {
    reads: {
      executor: 'ramping-arrival-rate', // fixed request rate, whatever the latency
      startRate: 10,
      timeUnit: '1s',
      preAllocatedVUs: USERS,
      maxVUs: USERS, // one VU per load-test account
      stages: [
        { target: 200, duration: '60s' }, // ramp 10 -> 200 req/s
        { target: 200, duration: '60s' }, // hold 200 req/s
      ],
    },
  },
  thresholds: {
    'http_req_duration{kind:read}': ['p(95)<100'], // NFR-PERF-01
    'http_req_failed{kind:read}': ['rate<0.01'],
    checks: ['rate>0.99'],
  },
  summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

function login(email) {
  const res = http.post(`${API}/auth/login`, JSON.stringify({ email, password: PASSWORD }),
    { headers: JSON_HEADERS, tags: { kind: 'login' } });
  if (res.status !== 204) fail(`login ${email}: ${res.status} ${res.body}`);
}

// setup runs once: wait for the API, then collect deal ids for "get deal".
export function setup() {
  for (let i = 0; i < 60; i++) {
    if (http.get(`${BASE}/readyz`, { tags: { kind: 'setup' } }).status === 200) break;
    sleep(1);
  }
  if (!PASSWORD) fail('SEED_OWNER_PASSWORD is required');
  login('owner@acme.demo');
  const res = http.get(`${API}/deals?limit=100`, { tags: { kind: 'setup' } });
  if (res.status !== 200) fail(`list deals: ${res.status}`);
  const dealIds = res.json('data').map((d) => d.id);
  if (dealIds.length === 0) fail('no deals: run the seed first');
  return { dealIds };
}

let loggedIn = false; // module state is per VU

const reads = [
  () => ['list contacts', `${API}/contacts?limit=20`],
  () => ['search contacts', `${API}/contacts?q=nguyen&limit=20`],
  (data) => ['get deal', `${API}/deals/${data.dealIds[exec.scenario.iterationInTest % data.dealIds.length]}`],
  () => ['dashboard pipeline', `${API}/dashboard/pipeline`],
];

export default function (data) {
  if (!loggedIn) {
    const n = String(((exec.vu.idInTest - 1) % USERS) + 1).padStart(3, '0');
    login(`loadtest-${n}@acme.demo`);
    loggedIn = true;
  }
  const [name, url] = reads[exec.scenario.iterationInTest % reads.length](data);
  const res = http.get(url, { tags: { kind: 'read', name } });
  check(res, { [`${name} 200`]: (r) => r.status === 200 });
}
