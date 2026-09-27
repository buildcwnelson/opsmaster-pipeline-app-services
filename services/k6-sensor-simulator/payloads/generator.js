// Synthetic physiological data generator for virtual wearable sensors

export function createDeviceRegistry(totalDevices = 1000) {
  const devices = [];
  for (let i = 1; i <= totalDevices; i++) {
    devices.push({
      deviceId: `dev-wearable-${String(i).padStart(5, '0')}`,
      patientId: `pat-${String(Math.floor((i - 1) / 2) + 1).padStart(5, '0')}`, // 2 sensors per patient
      sensorType: i % 4 === 0 ? "ecg" : (i % 3 === 0 ? "spo2" : "heart_rate"),
    });
  }
  return devices;
}

export function generateTelemetryPayload(device, options = {}) {
  const {
    injectAnomaly = false,
    injectMalformed = false,
  } = options;

  if (injectMalformed) {
    // Return intentionally malformed payload for canary resilience testing
    return {
      invalid_key: "corrupt_data",
      random_bytes: "0xdeadbeef",
    };
  }

  const now = new Date().toISOString();
  let metricType = device.sensorType;
  let value = null;
  let waveform = null;
  let unit = "";

  switch (metricType) {
    case "heart_rate":
      unit = "bpm";
      if (injectAnomaly) {
        // Tachycardia anomaly spike (>140 BPM)
        value = Math.floor(Math.random() * 40) + 145; // 145 - 185 BPM
      } else {
        // Normal sinus rhythm
        value = Math.floor(Math.random() * 30) + 65; // 65 - 95 BPM
      }
      break;

    case "spo2":
      unit = "%";
      if (injectAnomaly) {
        // Hypoxemia drop (<90%)
        value = Math.floor(Math.random() * 8) + 82; // 82 - 89%
      } else {
        // Normal healthy saturation
        value = Math.floor(Math.random() * 5) + 95; // 95 - 99%
      }
      break;

    case "ecg":
      metricType = "ecg_waveform";
      unit = "mV";
      if (injectAnomaly) {
        // Ventricular Arrhythmia high amplitude erratic spike
        waveform = Array.from({ length: 20 }, () => Number((Math.random() * 6.0 - 3.0).toFixed(3)));
      } else {
        // Typical P-Q-R-S-T simulated voltage sequence
        waveform = [0.05, 0.1, 0.08, -0.15, 1.25, -0.4, 0.05, 0.2, 0.25, 0.1, 0.05];
      }
      break;

    default:
      metricType = "heart_rate";
      unit = "bpm";
      value = 75;
  }

  const payload = {
    patient_id: device.patientId,
    device_id: device.deviceId,
    metric_type: metricType,
    unit: unit,
    timestamp: now,
    metadata: {
      firmware_version: "v2.4.1",
      battery_level: "92%",
      synthetic: "true",
      canary_test: "true",
    }
  };

  if (waveform) {
    payload.waveform = waveform;
  } else {
    payload.value = value;
  }

  return payload;
}
