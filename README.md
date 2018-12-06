 # rottie

 ## Features
  - Building blocks for an optimistic replication style system that ensures eventual consistency
  - Providing offline editing and real-time automatic synchronization
  - Using the document(JSON-like) as the basic data type
  - Stored documents can be searchable(the documents can be editable after attaching)

 ## Concept layout

 ```
  +--Client "A" (User)---------+
  | +--Document "D-1"--------+ |            +--Server------------------------+
  | | { a: 1, b: [], c: {} } | <-- CRDT --> | +--Collection "C-1"----------+ |
  | +------------------------+ |            | | +--Document "D-1"--------+ | |      +--Mongo DB---------------+
  +----------------------------+            | | | { a: 1, b: [], c: {} } | | |      | Snapshot for query      |
                                            | | +------------------------+ | | <--> | Snapshot with CRDT Meta |
  +--Client "A" (User)---------+            | | +--Document "D-2"--------+ | |      | Operations              |
  | +--Document "D-1"--------+ |            | | | { a: 1, b: [], c: {} } | | |      +-------------------------+
  | | { a: 2, b: [], c: {} } | <-- CRDT --> | | +------------------------+ | |
  | +------------------------+ |            | +----------------------------+ |
  +----------------------------+            +--------------------------------+
                                                             ^
  +--Client "C" (Admin)--------+                             |
  | +--Query "Q-1"-----------+ |                             |
  | | db.['c-1'].find(...)   | <-- Find Query ---------------+
  | +------------------------+ |
  +----------------------------+
 ```

 ## Example Usage

 Server API

 ```javascript
 const http = require('http');
 const WebSocket = require('ws');
 const rottie = require('rottie');
 const MongoClient = require('mongodb').MongoClient;

 // 01. Create a web server to serve files and listen to WebSocket connections
 const app = express();
 app.use(express.static('static'));
 const server = http.createServer(app);

 // 02. create rottie backend
 const backend = rottie.createFromMongo({
   client: new MongoClient('mongodb://localhost:27017'),
   dbName: 'myproject'
 });

 // 03. Connect any incoming WebSocket connection to rottie
 const wss = new WebSocket.Server({server: server});
 wss.on('connection', (ws, req) => {
   backend.listen(ws);
 });

 server.listen(8080);
 console.log('Listening on http://localhost:8080');
 ```

 Client API

 ```javascript
 // 01. create a client and connect to the server with it.
 const client = rottie.createClient({
   connection: rottie.createSocketIOConnection(io('http://localhost:8080'))
 });

 // 02. find a document and attach it
 const doc = await client.attach('counters', 'counter');

 // 03. subscribe local or remote change (e.g.: for rendering data)
 doc.subscribe((docEvent) => {
   console.log(docEvent); // e.g.: { message: 'set b to 3', change: {...} }
 });

 // 04. apply local change
 const isCommitted = doc.execute((replicatedJSON) => {
   replicatedJSON.increase();
 }, 'with user button');

 // 05. close
 client.close();
 ```
