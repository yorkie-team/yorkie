/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import http from "k6/http";
import { check, sleep } from "k6";
import { Counter, Rate, Trend } from "k6/metrics";

const API_URL = __ENV.API_URL || "http://localhost:8080";
const API_KEY = __ENV.API_KEY || "";
const PRESENCE_KEY_PREFIX = __ENV.PRESENCE_KEY_PREFIX || "test-presence";
const TEST_MODE = __ENV.TEST_MODE || "skew"; // skew | even
const CONCURRENCY = parseInt(__ENV.CONCURRENCY || "500", 10);
const VU_PER_PRESENCE = parseInt(__ENV.VU_PER_PRESENCE || "10", 10);
const ATTACH_ITERATIONS = parseInt(__ENV.ATTACH_ITERATIONS || "5", 10);

export const options = {
  stages: [
    { duration: "1m", target: CONCURRENCY }, // ramp up
    { duration: "1m", target: CONCURRENCY }, // maintain
    { duration: "30s", target: 0 }, // ramp down
  ],
  thresholds: {
    transaction_success_rate: ["rate>0.99"], // 99% success rate
    http_req_duration: ["p(95)<5000"], // 95% of requests under 5s
    active_clients_time: ["p(95)<500"], // 95% under 500ms
    attach_presence_time: ["p(95)<1000"], // 95% under 1s
    detach_presence_time: ["p(95)<1000"], // 95% under 1s
    transaction_time: ["p(95)<20000"], // 95% under 20s
  },
  setupTimeout: "30s",
  teardownTimeout: "30s",
};

// Custom metrics
const activeClients = new Counter("active_clients");
const activeClientsSuccessRate = new Rate("active_clients_success_rate");
const activeClientsTime = new Trend("active_clients_time", true);

const attachPresences = new Counter("attach_presences");
const attachPresencesSuccessRate = new Rate("attach_presences_success_rate");
const attachPresenceTime = new Trend("attach_presence_time", true);

const detachPresences = new Counter("detach_presences");
const detachPresencesSuccessRate = new Rate("detach_presences_success_rate");
const detachPresenceTime = new Trend("detach_presence_time", true);

const deactivateClients = new Counter("deactivate_clients");
const deactivateClientsSuccessRate = new Rate(
  "deactivate_clients_success_rate"
);
const deactivateClientsTime = new Trend("deactivate_clients_time", true);

const transactionFaileds = new Counter("transaction_faileds");
const transactionSuccessRate = new Rate("transaction_success_rate");
const transactionTime = new Trend("transaction_time", true);

const countMismatches = new Counter("count_mismatches");
const countMismatchRate = new Rate("count_mismatch_rate");

export default function () {
  const startTime = new Date().getTime();
  let success = false;
  const key = getKey();

  try {
    const [clientID, clientKey] = activateClient();

    for (let i = 0; i < ATTACH_ITERATIONS; i++) {
      const [presenceID] = attachPresence(clientID, key);

      sleep(1);

      detachPresence(clientID, presenceID, key);
    }

    deactivateClient(clientID, clientKey);

    success = true;
  } catch (error: any) {
    console.error(`Transaction failed: ${error.message}`);
    transactionFaileds.add(1);
  } finally {
    transactionSuccessRate.add(success);
    const endTime = new Date().getTime();
    transactionTime.add(endTime - startTime);
  }
}

function getKey() {
  if (TEST_MODE === "skew") return PRESENCE_KEY_PREFIX;
  const cnt = CONCURRENCY / VU_PER_PRESENCE;
  return `${PRESENCE_KEY_PREFIX}-${__VU % cnt}`;
}

// Common headers that can be reused across requests
function getCommonHeaders() {
  return {
    Accept: "*/*",
    "Accept-Language": "ko,en-US;q=0.9,en;q=0.8",
    "Cache-Control": "no-cache",
    Connection: "keep-alive",
    DNT: "1",
    Origin: "http://localhost:5173",
    Pragma: "no-cache",
    Referer: "http://localhost:5173/",
    "Sec-Fetch-Dest": "empty",
    "Sec-Fetch-Mode": "cors",
    "Sec-Fetch-Site": "same-site",
    "User-Agent":
      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36",
    "connect-protocol-version": "1",
    "content-type": "application/json",
    "sec-ch-ua":
      '"Chromium";v="136", "Google Chrome";v="136", "Not.A/Brand";v="99"',
    "sec-ch-ua-mobile": "?0",
    "sec-ch-ua-platform": '"macOS"',
    "x-api-key": API_KEY,
    "x-yorkie-user-agent": "@yorkie-js/sdk/0.6.12",
  };
}

// Helper function to make HTTP requests with common error handling
function makeRequest(url: string, payload: any, additionalHeaders = {}) {
  const headers = {
    ...getCommonHeaders(),
    ...additionalHeaders,
  };

  const params = { headers, timeout: "100s" };

  try {
    const resp = http.post(url, JSON.stringify(payload), params);

    const success = check(resp, {
      "status is 200": (r: any) => r.status === 200,
      "response has valid body": (r: any) => r.body && r.body.length > 0,
    });

    if (!success) {
      console.error(`Request failed: ${resp.status} ${resp.body}`);
      return null;
    }

    try {
      return JSON.parse(resp.body);
    } catch (e: any) {
      console.error(`Error parsing response: ${e.message}`);
      return null;
    }
  } catch (e: any) {
    console.error(`Exception in request: ${e.message}`);
    return null;
  }
}

function activateClient(): [string, string] {
  const startTime = new Date().getTime();

  // Generate a random client key
  const clientKey = `${Math.random()
    .toString(36)
    .substring(2)}-${Date.now().toString(36)}`;

  const resp = makeRequest(
    `${API_URL}/yorkie.v1.YorkieService/ActivateClient`,
    { clientKey },
    { "x-shard-key": `${API_KEY}/${clientKey}` }
  );

  if (!resp) {
    throw new Error("Failed to activate client");
  }

  const response: [string, string] = [resp.clientId, clientKey];

  const endTime = new Date().getTime();
  const duration = endTime - startTime;

  activeClients.add(1);
  activeClientsSuccessRate.add(true);
  activeClientsTime.add(duration);

  return response;
}

function deactivateClient(clientID: string, clientKey: string): void {
  const startTime = new Date().getTime();

  const resp = makeRequest(
    `${API_URL}/yorkie.v1.YorkieService/DeactivateClient`,
    { clientId: clientID },
    { "x-shard-key": `${API_KEY}/${clientKey}` }
  );
  if (!resp) {
    throw new Error("Failed to deactivate client");
  }

  const endTime = new Date().getTime();
  const duration = endTime - startTime;

  deactivateClients.add(1);
  deactivateClientsSuccessRate.add(true);
  deactivateClientsTime.add(duration);
}

function attachPresence(
  clientID: string,
  presenceKey: string
): [string, number] {
  const startTime = new Date().getTime();

  const payload = {
    clientId: clientID,
    presenceKey: presenceKey,
  };

  const resp = makeRequest(
    `${API_URL}/yorkie.v1.YorkieService/AttachPresence`,
    payload,
    { "x-shard-key": `${API_KEY}/${presenceKey}` }
  );
  if (!resp) {
    throw new Error("Failed to attach presence");
  }

  const presenceID = resp.presenceId;
  const count = Number(resp.count);

  const endTime = new Date().getTime();
  const duration = endTime - startTime;

  attachPresences.add(1);
  attachPresencesSuccessRate.add(true);
  attachPresenceTime.add(duration);

  return [presenceID, count];
}

function detachPresence(
  clientID: string,
  presenceID: string,
  presenceKey: string
): number {
  const startTime = new Date().getTime();

  const payload = {
    clientId: clientID,
    presenceId: presenceID,
    presenceKey: presenceKey,
  };

  const resp = makeRequest(
    `${API_URL}/yorkie.v1.YorkieService/DetachPresence`,
    payload,
    { "x-shard-key": `${API_KEY}/${presenceKey}` }
  );
  if (!resp) {
    throw new Error("Failed to detach presence");
  }

  const count = Number(resp.count);

  // In a concurrent environment, we can't predict the exact count
  // Only verify that the count is valid (non-negative)
  if (count < 0) {
    console.error(`Invalid count: ${count} (should not be negative)`);
    countMismatches.add(1);
    countMismatchRate.add(false);
  } else {
    countMismatchRate.add(true);
  }

  const endTime = new Date().getTime();
  const duration = endTime - startTime;

  detachPresences.add(1);
  detachPresencesSuccessRate.add(true);
  detachPresenceTime.add(duration);

  return count;
}
