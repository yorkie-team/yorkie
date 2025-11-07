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
const CHANNEL_KEY_PREFIX = __ENV.CHANNEL_KEY_PREFIX || "test-channel";
const TEST_MODE = __ENV.TEST_MODE || "skew"; // skew | even
const CONCURRENCY = parseInt(__ENV.CONCURRENCY || "500", 10);
const VU_PER_CHANNEL = parseInt(__ENV.VU_PER_CHANNEL || "10", 10);
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
    attach_channel_time: ["p(95)<1000"], // 95% under 1s
    detach_channel_time: ["p(95)<1000"], // 95% under 1s
    transaction_time: ["p(95)<20000"], // 95% under 20s
  },
  setupTimeout: "30s",
  teardownTimeout: "30s",
};

// Custom metrics
const activeClients = new Counter("active_clients");
const activeClientsSuccessRate = new Rate("active_clients_success_rate");
const activeClientsTime = new Trend("active_clients_time", true);

const attachChannels = new Counter("attach_channels");
const attachChannelsSuccessRate = new Rate("attach_channels_success_rate");
const attachChannelTime = new Trend("attach_channel_time", true);

const refreshChannels = new Counter("refresh_channels");
const refreshChannelsSuccessRate = new Rate("refresh_channels_success_rate");
const refreshChannelTime = new Trend("refresh_channel_time", true);

const detachChannels = new Counter("detach_channels");
const detachChannelsSuccessRate = new Rate("detach_channels_success_rate");
const detachChannelTime = new Trend("detach_channel_time", true);

const deactivateClients = new Counter("deactivate_clients");
const deactivateClientsSuccessRate = new Rate(
  "deactivate_clients_success_rate"
);
const deactivateClientsTime = new Trend("deactivate_clients_time", true);

const transactionFaileds = new Counter("transaction_faileds");
const transactionSuccessRate = new Rate("transaction_success_rate");
const transactionTime = new Trend("transaction_time", true);

export default function () {
  const startTime = new Date().getTime();
  let success = false;
  const key = getKey();

  try {
    const [clientID, clientKey] = activateClient();
    const [sessionID] = attachChannel(clientID, key);

    for (let i = 0; i < ATTACH_ITERATIONS; i++) {
      sleep(1);
      refreshChannel(clientID, key, sessionID);
    }
    detachChannel(clientID, key, sessionID);

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
  if (TEST_MODE === "skew") return CHANNEL_KEY_PREFIX;
  const cnt = CONCURRENCY / VU_PER_CHANNEL;
  return `${CHANNEL_KEY_PREFIX}-${__VU % cnt}`;
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

function attachChannel(clientID: string, channelKey: string): [string, number] {
  const startTime = new Date().getTime();

  const payload = {
    clientId: clientID,
    channelKey: channelKey,
  };

  const firstChannelKeyPath = channelKey.split(".")[0];

  const resp = makeRequest(
    `${API_URL}/yorkie.v1.YorkieService/AttachChannel`,
    payload,
    { "x-shard-key": `${API_KEY}/${firstChannelKeyPath}` }
  );
  if (!resp) {
    throw new Error("Failed to attach channel");
  }

  const sessionID = resp.sessionId;
  const count = Number(resp.count);

  const endTime = new Date().getTime();
  const duration = endTime - startTime;

  attachChannels.add(1);
  attachChannelsSuccessRate.add(true);
  attachChannelTime.add(duration);

  return [sessionID, count];
}

function refreshChannel(
  clientID: string,
  channelKey: string,
  sessionID: string
): number {
  const startTime = new Date().getTime();

  const payload = {
    clientId: clientID,
    channelKey: channelKey,
    sessionId: sessionID,
  };

  const firstChannelKeyPath = channelKey.split(".")[0];

  const resp = makeRequest(
    `${API_URL}/yorkie.v1.YorkieService/RefreshChannel`,
    payload,
    { "x-shard-key": `${API_KEY}/${firstChannelKeyPath}` }
  );
  if (!resp) {
    throw new Error("Failed to refresh channel");
  }

  const count = Number(resp.count);

  const endTime = new Date().getTime();
  const duration = endTime - startTime;

  refreshChannels.add(1);
  refreshChannelsSuccessRate.add(true);
  refreshChannelTime.add(duration);

  return count;
}

function detachChannel(
  clientID: string,
  channelKey: string,
  sessionID: string
): number {
  const startTime = new Date().getTime();

  const payload = {
    clientId: clientID,
    channelKey: channelKey,
    sessionId: sessionID,
  };

  const firstChannelKeyPath = channelKey.split(".")[0];

  const resp = makeRequest(
    `${API_URL}/yorkie.v1.YorkieService/DetachChannel`,
    payload,
    { "x-shard-key": `${API_KEY}/${firstChannelKeyPath}` }
  );
  if (!resp) {
    throw new Error("Failed to detach channel");
  }

  const count = Number(resp.count);

  const endTime = new Date().getTime();
  const duration = endTime - startTime;

  detachChannels.add(1);
  detachChannelsSuccessRate.add(true);
  detachChannelTime.add(duration);

  return count;
}
