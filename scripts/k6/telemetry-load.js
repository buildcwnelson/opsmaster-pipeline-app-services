import http from "k6/http";
import { check, sleep } from "k6";
import { Counter, Rate, Trend } from "k6/metrics";

// Configuration via environment variables
const TARGET_URL = __ENV.TARGET_URL || "http://localhost:8080/api/v1/vitals";
const TOTAL_VIRTUAL_DEVICES = parseInt(__ENV.VIRTUAL_DEVICES || "1000", 10);
const INJECT_ANOMALIES = __ENV.INJECT_ANOMALIES === "true";
const INJECT_MALFORMED_RATE = parseFloat(__ENV.INJECT_MALFORMED_RATE || "0.0");
const INJECT_LATENCY = __ENV.INJECT_LATENCY === "true";

// Custom Prometheus-compatible metrics in k6
export const vitalsSentCounter = new Counter("opsmaster_k6_vitals_sent_total");
export const anomaliesInjectedCounter = new Counter("opsmaster_k6_anomalies_injected_total");
export const errorsInjectedCounter = new Counter("opsmaster_k6_errors_injected_total");
export const ingestSuccessRate = new Rate("opsmaster_k6_ingest_success_rate");
export const ingestLatencyTrend = new Trend("opsmaster_k6_ingest_duration_ms");

export const options = {
  scenarios: {
    canary_sensor_workload: {
      executor: "ramping-vus",
      startVUs: 50,
      stages: [
        { duration: "15s", target: 200 },   // Warm-up ramp
        { duration: "30s", target: 500 },   // Ramp to 500 virtual sensors
        { duration: "45s", target: 1000 },  // Peak 1,000+ virtual sensors
        { duration: "30s", target: 1000 },  // Sustained telemetry flow
        { duration: "15s", target: 0 },     // Ramp-down
      ],
      gracefulRampDown: "10s",
    },
  },
  thresholds: {
    http_req_failed: ["rate<0.01"],          // Error rate must be < 1%
    http_req_duration: ["p(95)<200"],        // 95% of requests must complete in < 200ms
    opsmaster_k6_ingest_success_rate: ["rate>0.99"],
  },
};

// Virtual wearable device registry
function createDeviceRegistry(count) {
  const list = [];
  for (let i = 1; i <= count; i++) {
    list.push({
      deviceId: `dev-wearable-${String(i).padStart(5, "0")}`,
      patientId: `pat-${String(((i - 1) % 3) + 1).padStart(3, "0")}`, // Matches seeded patients pat-001, pat-002, pat-003
      sensorType: i % 4 === 0 ? "ecg_waveform" : (i % 3 === 0 ? "spo2" : "heart_rate"),
    });
  }
  return list;
}

const devices = createDeviceRegistry(TOTAL_VIRTUAL_DEVICES);

export default function () {
  const deviceIndex = (__VU * 100 + __ITER) % devices.length;
  const device = devices[deviceIndex];

  const shouldInjectAnomaly = INJECT_ANOMALIES && Math.random() < 0.15;
  const shouldInjectMalformed = Math.random() < INJECT_MALFORMED_RATE;

  if (shouldInjectAnomaly) anomaliesInjectedCounter.add(1);
  if (shouldInjectMalformed) errorsInjectedCounter.add(1);

  if (INJECT_LATENCY) {
    sleep(Math.random() * 0.2 + 0.1);
  }

  let payload;
  if (shouldInjectMalformed) {
    payload = { corrupt_key: "corrupt_data", timestamp: "not-a-timestamp" };
  } else {
    payload = generatePayload(device, shouldInjectAnomaly);
  }

  const params = {
    headers: {
      "Content-Type": "application/json",
      "X-Request-ID": `k6-${device.deviceId}-${Date.now()}`,
      "X-Device-ID": device.deviceId,
    },
    timeout: "5s",
  };

  const startTime = Date.now();
  const res = http.post(TARGET_URL, JSON.stringify(payload), params);
  const latency = Date.now() - startTime;

  ingestLatencyTrend.add(latency);

  if (shouldInjectMalformed) {
    const isRejected = check(res, { "malformed payload rejected (400)": (r) => r.status === 400 });
    ingestSuccessRate.add(isRejected);
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
    if (isSuccess) vitalsSentCounter.add(1);
  }

  sleep(Math.random() * 0.5 + 0.8);
}

function generatePayload(device, injectAnomaly) {
  const now = new Date().toISOString();
  let metricType = device.sensorType;
  let value = null;
  let waveform = null;
  let unit = "";

  switch (metricType) {
    case "heart_rate":
      unit = "bpm";
      value = injectAnomaly
        ? Math.floor(Math.random() * 40) + 145 // 145-185 BPM (Tachycardia anomaly)
        : Math.floor(Math.random() * 30) + 65;  // 65-95 BPM (Normal)
      break;

    case "spo2":
      unit = "%";
      value = injectAnomaly
        ? Math.floor(Math.random() * 8) + 82   // 82-89% (Hypoxemia anomaly)
        : Math.floor(Math.random() * 5) + 95;  // 95-99% (Normal)
      break;

    case "ecg_waveform":
      unit = "mV";
      waveform = injectAnomaly
        ? Array.from({ length: 20 }, () => Number((Math.random() * 6.0 - 3.0).toFixed(3))) // Erratic burst
        : [0.05, 0.1, 0.08, -0.15, 1.25, -0.4, 0.05, 0.2, 0.25, 0.1, 0.05];                 // Sinus wave
      break;

    default:
      metricType = "heart_rate";
      unit = "bpm";
      value = 75;
  }

  const data = {
    patient_id: device.patientId,
    device_id: device.deviceId,
    metric_type: metricType,
    unit: unit,
    timestamp: now,
    metadata: {
      firmware_version: "v2.5.0",
      battery_level: "94%",
      simulated: "true",
    },
  };

  if (waveform) {
    data.waveform = waveform;
  } else {
    data.value = value;
  }

  return data;
}

export function handleSummary(data) {
  const p95 = data.metrics.http_req_duration ? data.metrics.http_req_duration.values["p(95)"] : 0;
  const failRate = data.metrics.http_req_failed ? data.metrics.http_req_failed.values.rate : 0;
  const totalReqs = data.metrics.http_reqs ? data.metrics.http_reqs.values.count : 0;
  const passed = p95 < 200 && failRate < 0.01;

  console.log("\n========================================================");
  console.log("       OPSMASTER 3-CONTAINER LOAD TEST SUMMARY          ");
  console.log("========================================================");
  console.log(`Total Requests:         ${totalReqs}`);
  console.log(`http_req_duration p(95): ${p95.toFixed(2)} ms (SLA < 200ms)`);
  console.log(`http_req_failed rate:   ${(failRate * 100).toFixed(2)} % (SLA < 1%)`);
  console.log(`Canary Status:          ${passed ? "PASSED (Promoted)" : "FAILED (Rollback)"}`);
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
