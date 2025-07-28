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
import { b64encode } from "k6/encoding";
import { check, sleep, group } from "k6";
import { Counter, Rate, Trend } from "k6/metrics";

// API URL
const API_URL = __ENV.API_URL || "http://localhost:8080";

// API Key
const API_KEY = __ENV.API_KEY || "";

// Document key prefix
const DOC_KEY_PREFIX = __ENV.DOC_KEY_PREFIX || "test-presence";

// Test mode: 'skew' or 'even'
const TEST_MODE = __ENV.TEST_MODE || "skew";

// Virtual users(VUs) concurrency for the test
const CONCURRENCY = parseInt(__ENV.CONCURRENCY || "500");

// Virtual users per document (for even distribution)
const VU_PER_DOCS = parseInt(__ENV.VU_PER_DOCS || "10");

// In 'skew' mode, use a single document; in 'even' mode, distribute VUs across documents
function getDocKey() {
  if (TEST_MODE === "skew") {
    return DOC_KEY_PREFIX;
  }

  // 'even' mode
  const docCount = CONCURRENCY / VU_PER_DOCS;
  // Distribute documents evenly based on virtual user ID
  return `${DOC_KEY_PREFIX}-${__VU % docCount}`;
}

// Custom metrics
const activeClients = new Counter("active_clients");
const activeClientsSuccessRate = new Rate("active_clients_success_rate");
const activeClientsTime = new Trend("active_clients_time", true); // true enables time formatting

const attachDocuments = new Counter("attach_documents");
const attachDocumentsSuccessRate = new Rate("attach_documents_success_rate");
const attachDocumentsTime = new Trend("attach_documents_time", true); // true enables time formatting

const pushpulls = new Counter("pushpulls");
const pushpullsSuccessRate = new Rate("pushpulls_success_rate");
const pushpullsTime = new Trend("pushpulls_time", true); // true enables time formatting

const deactivateClients = new Counter("deactivate_clients");
const deactivateClientsSuccessRate = new Rate(
  "deactivate_clients_success_rate"
);
const deactivateClientsTime = new Trend("deactivate_clients_time", true); // true enables time formatting

const transactionFaileds = new Counter("transaction_faileds");
const transactionSuccessRate = new Rate("transaction_success_rate");
const transactionTime = new Trend("transaction_time", true); // true enables time formatting

let currentLamport = 1;
let knownVersionVector: Record<string, number> = {};
let myClientID = "";

function getVersionVector(clientID: string) {
  const vv = { ...knownVersionVector };
  vv[clientID] = currentLamport;
  return vv;
}

// k6 options for load testing
export const options = {
  stages: [
    { duration: "1m", target: CONCURRENCY }, // Ramp up to concurrenct(default: 500) users in 1 minute
    { duration: "1m", target: CONCURRENCY }, // Maintain concurrent users(default: 500) for 1 minute
    { duration: "30s", target: 0 }, // Ramp down to 0 users in 30 seconds
  ],
  thresholds: {
    transaction_success_rate: ["rate>0.99"], // Maintain 99% success rate
    http_req_duration: ["p(95)<10000"], // 95% of requests complete within 10 seconds
    // Add specific thresholds for our custom timing metrics
    active_clients_time: ["p(95)<500"], // 95% under 500ms
    attach_documents_time: ["p(95)<10000"], // 95% under 10s
    pushpulls_time: ["p(95)<1000"], // 95% under 1s
    deactivate_clients_time: ["p(95)<10000"], // 95% under 10s
    transaction_time: ["p(95)<30000"], // 95% under 30s
  },
  // System resource settings
  setupTimeout: "30s",
  teardownTimeout: "30s",
};

export default function () {
  const startTime = new Date().getTime();
  let success = false;
  const docKey = getDocKey();

  group("Presence", function () {
    try {
      const [clientID, clientKey] = activateClient();
      const [docID, serverSeq] = attachDocument(
        clientID,
        hexToBase64(clientID),
        docKey
      );

      sleep(1); // Simulate some processing time

      let lastServerSeq = serverSeq;
      // Randomly push and pull changes to the document to emulate presence updates
      for (let i = 0; i < 5; i++) {
        [, lastServerSeq] = pushpullChanges(
          clientID,
          docID,
          docKey,
          lastServerSeq
        );
        sleep(1); // Simulate some processing time
      }

      deactivateClient(clientID, clientKey);

      success = true;
    } catch (error: any) {
      transactionFaileds.add(1);
    } finally {
      transactionSuccessRate.add(success);
      const endTime = new Date().getTime();
      transactionTime.add(endTime - startTime);
    }
  });
}

function hexToBase64(hex: string) {
  const bytes = new Uint8Array(
    hex.match(/.{1,2}/g)?.map((byte) => parseInt(byte, 16)) || []
  );
  return b64encode(bytes, "rawStd");
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
    const response = http.post(url, JSON.stringify(payload), params);

    const success = check(response, {
      "status is 200": (r: any) => r.status === 200,
      "response has valid body": (r: any) => r.body && r.body.length > 0,
    });

    if (!success) {
      console.error(`Request failed: ${response.status} ${response.body}`);
      return null;
    }

    try {
      return JSON.parse(response.body);
    } catch (e: any) {
      console.error(`Error parsing response: ${e.message}`);
      return null;
    }
  } catch (e: any) {
    console.error(`Exception in request: ${e.message}`);
    return null;
  }
}

function activateClient() {
  const startTime = new Date().getTime();

  // Generate a random client key
  const clientKey = `${Math.random()
    .toString(36)
    .substring(2)}-${Date.now().toString(36)}`;

  const result = makeRequest(
    `${API_URL}/yorkie.v1.YorkieService/ActivateClient`,
    { clientKey },
    { "x-shard-key": `${API_KEY}/${clientKey}` }
  );

  const response = [result!.clientId, clientKey];

  const endTime = new Date().getTime();
  const duration = endTime - startTime;

  activeClients.add(1);
  activeClientsSuccessRate.add(true);
  activeClientsTime.add(duration);

  return response;
}

function deactivateClient(clientID: string, clientKey: string): string {
  const startTime = new Date().getTime();

  const result = makeRequest(
    `${API_URL}/yorkie.v1.YorkieService/DeactivateClient`,
    { clientId: clientID },
    { "x-shard-key": `${API_KEY}/${clientKey}` }
  );

  result!.clientId;

  const endTime = new Date().getTime();
  const duration = endTime - startTime;

  deactivateClients.add(1);
  deactivateClientsSuccessRate.add(true);
  deactivateClientsTime.add(duration);

  return clientID;
}

function attachDocument(clientID: string, actorID: string, docKey: string) {
  const startTime = new Date().getTime();

  // Generate a random color for presence data
  const colors = ["#00C9A7", "#C0392B", "#3498DB", "#F1C40F", "#9B59B6"];
  const randomColor = colors[Math.floor(Math.random() * colors.length)];

  const base64ClientID = hexToBase64(clientID);
  myClientID = base64ClientID;
  const versionVector = getVersionVector(base64ClientID);

  const payload = {
    clientId: clientID,
    changePack: {
      documentKey: docKey,
      checkpoint: { clientSeq: 1 },
      changes: [
        {
          id: { clientSeq: 1, actorId: actorID, versionVector },
          presenceChange: {
            type: "CHANGE_TYPE_PUT",
            presence: { data: { color: `"${randomColor}"` } },
          },
        },
      ],
      versionVector,
    },
  };

  const resp = makeRequest(
    `${API_URL}/yorkie.v1.YorkieService/AttachDocument`,
    payload,
    { "x-shard-key": `${API_KEY}/${docKey}` }
  );

  if (resp.changePack.versionVector) {
    knownVersionVector = { ...knownVersionVector, ...resp.changePack.versionVector };
  }

  const documentId = resp!.documentId;
  const serverSeq = Number(resp!.changePack.checkpoint.serverSeq);

  const endTime = new Date().getTime();
  const duration = endTime - startTime;

  attachDocuments.add(1);
  attachDocumentsSuccessRate.add(true);
  attachDocumentsTime.add(duration);

  return [documentId, serverSeq];
}

function pushpullChanges(
  clientID: string,
  docID: string,
  docKey: string,
  lastServerSeq: number
) {
  const startTime = new Date().getTime();
  currentLamport++; 
  const versionVector = getVersionVector(myClientID);

  const payload = {
    clientId: clientID,
    documentId: docID,
    changePack: {
      documentKey: docKey,
      checkpoint: {
        clientSeq: 1,
        serverSeq: lastServerSeq,
      },
      versionVector,
    },
  };

  const resp = makeRequest(
    `${API_URL}/yorkie.v1.YorkieService/PushPullChanges`,
    payload,
    { "x-shard-key": `${API_KEY}/${docKey}` }
  );

  if (resp.changePack.versionVector) {
    knownVersionVector = { ...knownVersionVector, ...resp.changePack.versionVector };
  }

  const documentId = resp!.documentId;
  const serverSeq = Number(resp!.changePack.checkpoint.serverSeq);

  const endTime = new Date().getTime();
  const duration = endTime - startTime;

  pushpulls.add(1);
  pushpullsSuccessRate.add(true);
  pushpullsTime.add(duration);

  return [documentId, serverSeq];
}
