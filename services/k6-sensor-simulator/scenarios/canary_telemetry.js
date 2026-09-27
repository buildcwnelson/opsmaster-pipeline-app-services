import http from "k6/http";
import { check, sleep } from "k6";
import { Counter, Rate, Trend } from "k6/metrics";
import { createDeviceRegistry, generateTelemetryPayload } from "../payloads/generator.js";

// Configurable parameters via environment variables
const TARGET_URL = __ENV.TARGET_URL || "http://localhost:8080/api/v1/telemetry";
const TOTAL_VIRTUAL_DEVICES = parseInt(__ENV.VIRTUAL_DEVICES || "1000", 10);
const INJECT_ANOMALIES = __ENV.INJECT_ANOMALIES === "true";
const INJECT_MALFORMED_RATE = parseFloat(__ENV.INJECT_MALFORMED_RATE || "0.0"); // e.g. 0.005 for 0.5%
const INJECT_LATENCY = __ENV.INJECT_LATENCY === "true";

// Custom Prometheus-compatible metrics in k6
export const vitalsSentCounter = new Counter("opsmaster_k6_vitals_sent_total");
export const anomaliesInjectedCounter = new Counter("opsmaster_k6_anomalies_injected_total");
export const errorsInjectedCounter = new Counter("opsmaster_k6_errors_injected_total");
export const ingestSuccessRate = new Rate("opsmaster_k6_ingest_success_rate");
export const ingestLatencyTrend = new Trend("opsmaster_k6_ingest_duration_ms");

export const options = {
  scenarios: {
    // 1,000+ virtual wearable sensors telemetry ramp-up and steady state
    canary_sensor_workload: {
      executor: "ramping-vus",
      startVUs: 50,
      stages: [
        { duration: "20s", target: 200 },   // Warm-up ramp
        { duration: "40s", target: 500 },   // Ramp to 500 virtual sensors
        { duration: "1m",  target: 1000 },  // Full scale: 1,000+ virtual sensors
        { duration: "30s", target: 1000 },  // Sustained peak load
        { duration: "20s", target: 0 },     // Ramp-down
      ],
      gracefulRampDown: "10s",
    },
  },
  thresholds: {
    // Strict SLA thresholds specified in requirements
    http_req_failed: ["rate<0.01"],          // Error rate must be < 1%
    http_req_duration: ["p(95)<200"],        // 95% of requests must complete in < 200ms
    opsmaster_k6_ingest_success_rate: ["rate>0.99"],
  },
};

// Global device registry initialized once per test run
const devices = createDeviceRegistry(TOTAL_VIRTUAL_DEVICES);

export default function () {
  // Select a device deterministically or pseudo-randomly per VU and iteration
  const deviceIndex = (__VU * 100 + __ITER) % devices.length;
  const device = devices[deviceIndex];

  // Optional error & anomaly injection for canary validation
  const shouldInjectAnomaly = INJECT_ANOMALIES && Math.random() < 0.15; // 15% anomaly rate if flag is on
  const shouldInjectMalformed = Math.random() < INJECT_MALFORMED_RATE;

  if (shouldInjectAnomaly) {
    anomaliesInjectedCounter.add(1);
  }
  if (shouldInjectMalformed) {
    errorsInjectedCounter.add(1);
  }

  // Inject artificial client latency if flag enabled
  if (INJECT_LATENCY) {
    sleep(Math.random() * 0.2 + 0.1); // 100-300ms jitter
  }

  const payload = generateTelemetryPayload(device, {
    injectAnomaly: shouldInjectAnomaly,
    injectMalformed: shouldInjectMalformed,
  });

  const params = {
    headers: {
      "Content-Type": "application/json",
      "X-Request-ID": `k6-sim-${device.deviceId}-${Date.now()}`,
      "X-Device-ID": device.deviceId,
    },
    timeout: "5s",
  };

  const startTime = Date.now();
  const res = http.post(TARGET_URL, JSON.stringify(payload), params);
  const latency = Date.now() - startTime;

  ingestLatencyTrend.add(latency);

  // When intentionally injecting malformed payloads, expect 400 Bad Request
  if (shouldInjectMalformed) {
    const isMalformedRejected = check(res, {
      "malformed payload rejected (400)": (r) => r.status === 400,
    });
    ingestSuccessRate.add(isMalformedRejected);
  } else {
    const isSuccess = check(res, {
      "status is 200 or 202": (r) => r.status === 200 || r.status === 202,
      "response has accepted count": (r) => {
        try {
          const body = JSON.parse(r.body);
          return body.accepted >= 1 || body.status === "accepted";
        } catch (e) {
          return false;
        }
      },
    });

    ingestSuccessRate.add(isSuccess);
    if (isSuccess) {
      vitalsSentCounter.add(1);
    }
  }

  // Cadence: wearable sensors typically emit heart rate / SpO2 every 1 - 2 seconds
  sleep(Math.random() * 0.5 + 0.8);
}

export function handleSummary(data) {
  const p95 = data.metrics.http_req_duration ? data.metrics.http_req_duration.values["p(95)"] : 0;
  const failRate = data.metrics.http_req_failed ? data.metrics.http_req_failed.values.rate : 0;
  const totalReqs = data.metrics.http_reqs ? data.metrics.http_reqs.values.count : 0;

  const passed = p95 < 200 && failRate < 0.01;

  console.log("\n========================================================");
  console.log("       OPSMASTER CANARY SENSOR LOAD TEST SUMMARY        ");
  console.log("========================================================");
  console.log(`Total Requests:         ${totalReqs}`);
  console.log(`http_req_duration p(95): ${p95.toFixed(2)} ms (Threshold: < 200ms)`);
  console.log(`http_req_failed rate:   ${(failRate * 100).toFixed(2)} % (Threshold: < 1%)`);
  console.log(`Canary SLA Status:      ${passed ? "PASSED (Canary Promoted)" : "FAILED (Canary Rollback)"}`);
  console.log("========================================================\n");

  return {
    stdout: JSON.stringify({
      timestamp: new Date().toISOString(),
      canary_passed: passed,
      total_requests: totalReqs,
      p95_duration_ms: p95,
      failure_rate: failRate,
    }, null, 2),
  };
}
