// Steady checkout load (default) or a spike to stress the trace collector (SPIKE=1).
// Hits the Gateway by IP with a Host header: *.localhost resolution outside browsers/curl is unreliable.
import http from 'k6/http';
import { check } from 'k6';

const steady = {
  executor: 'constant-arrival-rate',
  rate: Number(__ENV.RATE || 20), timeUnit: '1s',
  duration: __ENV.DURATION || '10m',
  preAllocatedVUs: 20, maxVUs: 100,
};

// Ramp far past steady so the gateway collector's tail-sampling buffer (10s of traces) grows fast.
const spike = {
  executor: 'ramping-arrival-rate',
  startRate: 20, timeUnit: '1s',
  stages: [
    { target: Number(__ENV.RATE || 1000), duration: '1m' },
    { target: Number(__ENV.RATE || 1000), duration: '3m' },
    { target: 20, duration: '30s' },
  ],
  preAllocatedVUs: 200, maxVUs: 1000,
};

export const options = {
  scenarios: { checkout: __ENV.SPIKE ? spike : steady },
  // The spike is about the collector, not the shop, so only the steady run gates on errors.
  thresholds: __ENV.SPIKE ? {} : { http_req_failed: ['rate<0.01'] },
};

const url = `${__ENV.BASE_URL || 'http://127.0.0.1:8080'}/buy`;

export default function () {
  // Email exercises the collector's redaction processor (hashed in Tempo).
  const res = http.post(url, JSON.stringify({ email: `user${__VU}@example.com` }), {
    headers: { Host: 'shop.localhost', 'Content-Type': 'application/json' },
  });
  check(res, { 'status 200': (r) => r.status === 200 });
}
