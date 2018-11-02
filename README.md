# rotty

## Concept layout

```
 +--Client "A"----------------+
 | +--Document "D-1"--------+ |            +--Server------------------------+
 | | { a: 1, b: [], c: {} } | <-- CRDT --> | +--Collection "C-1"----------+ |
 | +------------------------+ |            | | +--Document "D-1"--------+ | |
 +----------------------------+            | | | { a: 1, b: [], c: {} } | | |      +--Mongo--------+
                                           | | +------------------------+ | | <--> | Snapshot      |
 +--Client "B"----------------+            | | +--Document "D-2"--------+ | |      | Meta(Ops,...) |
 | +--Document "D-1"--------+ |            | | | { a: 1, b: [], c: {} } | | |      +---------------+
 | | { a: 2, b: [], c: {} } | <-- CRDT --> | | +------------------------+ | |      
 | +------------------------+ |            | +----------------------------+ |
 +----------------------------+            +--------------------------------+
                                                            ^
 +--Client "C" ---------------+                             |
 | +--Query "Q-1"-----------+ |                             |
 | | db.['c-1'].find(...)   | <-- Snapshot Query -----------+
 | +------------------------+ |
 +----------------------------+
```

## Examples

backend

```javascript
const http = require('http');
const WebSocket = require('ws');
const rotty = require('rotty');
const MongoClient = require('mongodb').MongoClient;

// 01. Create a web server to serve files and listen to WebSocket connections
const app = express();
app.use(express.static('static'));
const server = http.createServer(app);

// 02. create rotty backend
const backend = rotty.createFromMongo({
  client: new MongoClient('mongodb://localhost:27017'),
  dbName: 'myproject'
});
  
// 03. Connect any incoming WebSocket connection to rotty
const wss = new WebSocket.Server({server: server});
wss.on('connection', (ws, req) => {
  backend.listen(ws);
});

server.listen(8080);
console.log('Listening on http://localhost:8080');
```

frontend

```javascript
// 01. create a client and connect to the server with it.
const client = rotty.createClient({
  socket: new WebSocket('ws://localhost:8080')
});
await client.connect();

// 02. find a document and attach it
const doc1 = await client.collection('documents').attachOne({_id: 'D-1'});

// 03. subscribe remote change (e.g.: for rendering data)
doc1.subscribe((change) => {
  console.log(change); // e.g.: { message: 'set b to 3', change: {...}, doc: { a: 2, b: 3}}
});

// 04. apply local change
doc1.change('set a to 2', doc => {
  doc.set('a', 2); // { a: 2 }
});

// 05. close
client.close();
```
