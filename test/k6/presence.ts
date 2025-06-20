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

// Control document key (for interference testing in skew mode)
const CONTROL_DOC_KEY = "control-doc";

// Test mode: 'skew' or 'even'
const TEST_MODE = __ENV.TEST_MODE || "skew";

// Virtual users(VUs) concurrency for the test
const CONCURRENCY = parseInt(__ENV.CONCURRENCY || "500");

// Virtual users per document (for even distribution)
const VU_PER_DOCS = parseInt(__ENV.VU_PER_DOCS || "10");

// Whether to use control document (for interference testing in skew mode)
const USE_CONTROL_DOC = __ENV.USE_CONTROL_DOC === "true";

// In 'skew' mode, use a single document; in 'even' mode, distribute VUs across documents
function getDocKey() {
  if (TEST_MODE === "skew") {
    return DOC_KEY_PREFIX;
  } else {
    // 'even' mode
    const documentCount = CONCURRENCY / VU_PER_DOCS;
    // Distribute documents evenly based on virtual user ID
    return `${DOC_KEY_PREFIX}-${__VU % documentCount}`;
  }
}

// Custom metrics
const activeClients = new Counter("active_clients");
const attachDocuments = new Counter("attach_documents");
const pushpulls = new Counter("pushpulls");

const transactionFaileds = new Counter("transaction_faileds");
const transactionSuccessRate = new Rate("transaction_success_rate");
const transactionTime = new Trend("transaction_time");
const controlDocLatency = new Trend("control_doc_latency"); // Metrics for measuring interference

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
    control_doc_latency: ["p(95)<500"], // Control document response time(Threshold for interference testing)
  },
  // System resource settings
  setupTimeout: "30s",
  teardownTimeout: "30s",
};

export default function () {
  const startTime = new Date().getTime();
  let success = false;
  const docKey = getDocKey();

  group("Attachment", function () {
    try {
      const [clientID, clientKey] = activateClient();
      activeClients.add(1);

      const [docID, serverSeq] = attachDocument(
        clientID,
        hexToBase64(clientID),
        docKey
      );
      success = docID !== "";
      if (success) {
        attachDocuments.add(1);
      }

      let lastServerSeq = serverSeq;
      // Randomly push and pull changes to the document to emulate presence updates
      for (let i = 0; i < Math.random() * 3 + 2; i++) {
        [, lastServerSeq] = pushpullChanges(
          clientID,
          docID,
          docKey,
          lastServerSeq
        );
        pushpulls.add(1);
        sleep(1); // Sleep for 1 second between changes
      }

      // Skew interference test: measuring control document response time under load
      if (USE_CONTROL_DOC && __VU % 10 === 0) {
        // 10% sampling
        const controlStartTime = new Date().getTime();
        const controlDocID = attachDocument(
          clientID,
          hexToBase64(clientID),
          CONTROL_DOC_KEY
        );
        const controlEndTime = new Date().getTime();
        controlDocLatency.add(controlEndTime - controlStartTime);
      }

      deactivateClient(clientID, clientKey);
    } catch (error: any) {
      console.error(`Error in main function: ${error.message}`);
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
  return b64encode(bytes);
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

  const params = {
    headers,
    timeout: "10s",
  };

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
  // Generate a random client key
  const clientKey = `${Math.random()
    .toString(36)
    .substring(2)}-${Date.now().toString(36)}`;
  const url = `${API_URL}/yorkie.v1.YorkieService/ActivateClient`;

  const result = makeRequest(
    url,
    { clientKey },
    { "x-shard-key": `${API_KEY}/${clientKey}` }
  );

  return [result!.clientId, clientKey];
}

function deactivateClient(clientID: string, clientKey: string): string {
  const url = `${API_URL}/yorkie.v1.YorkieService/DeactivateClient`;

  const result = makeRequest(
    url,
    { clientId: clientID },
    { "x-shard-key": `${API_KEY}/${clientKey}` }
  );

  return result!.clientId;
}

function attachDocument(clientID: string, actorID: string, docKey: string) {
  const url = `${API_URL}/yorkie.v1.YorkieService/AttachDocument`;

  // Generate a random color for presence data
  const colors = ["#00C9A7", "#C0392B", "#3498DB", "#F1C40F", "#9B59B6"];
  const randomColor = colors[Math.floor(Math.random() * colors.length)];

  const payload = {
    clientId: clientID,
    changePack: {
      documentKey: docKey,
      checkpoint: {
        clientSeq: 1,
      },
      changes: [
        {
          id: {
            clientSeq: 1,
            actorId: actorID,
            versionVector: {},
          },
          presenceChange: {
            type: "CHANGE_TYPE_PUT",
            presence: {
              data: ["color", `"${randomColor}"`],
            },
          },
        },
      ],
      versionVector: {},
    },
  };

  const resp = makeRequest(url, payload, {
    "x-shard-key": `${API_KEY}/${docKey}`,
  });

  const documentId = resp!.documentId;
  const serverSeq = Number(resp!.changePack.checkpoint.serverSeq);
  return [documentId, serverSeq];
}

function pushpullChanges(
  clientID: string,
  docID: string,
  docKey: string,
  lastServerSeq: number
) {
  const url = `${API_URL}/yorkie.v1.YorkieService/PushPullChanges`;

  const payload = {
    clientId: clientID,
    documentId: docID,
    changePack: {
      documentKey: docKey,
      checkpoint: {
        clientSeq: 1,
        serverSeq: lastServerSeq,
      },
      versionVector: {},
    },
  };

  const resp = makeRequest(url, payload, {
    "x-shard-key": `${API_KEY}/${docKey}`,
  });

  const documentId = resp!.documentId;
  const serverSeq = Number(resp!.changePack.checkpoint.serverSeq);
  return [documentId, serverSeq];
}
