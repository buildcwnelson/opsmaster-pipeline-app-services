FROM grafana/k6:latest

WORKDIR /scripts

COPY . /scripts

ENV TARGET_URL="http://vitals-ingress-api:8080/api/v1/telemetry"
ENV VIRTUAL_DEVICES="1000"

ENTRYPOINT ["k6", "run", "/scripts/scenarios/canary_telemetry.js"]
