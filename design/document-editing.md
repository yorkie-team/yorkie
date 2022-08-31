---
title: document-editing
target-version: 0.1.0
---

# Document Editing

This document covers document editing executed in the SDK.

## Summary

Document Editing is a mechanism to modify the document. It consists of two parts, Local and Remote.
We will provide a simple document editing example in the SDK to show how it works.

### Goals

The purpose of this document is to help new SDK contributors to understand the SDK's editing behavior.

### Non-Goals

This document does not describe algorithms such as CRDTs or logical clocks.

## Proposal Details

First, we briefly describe `Client` and `Document` and then explain what happens inside the Client and Document when editing a document.

### Overview

A high-level overview of Yorkie is as follows:

![document-editing-overview](media/document-editing-overview.png)

A description of the main components is as follows:
- `SDK` consists of `Client` and `Document`.
- `Client`: A `Client` is a normal client that can communicate with `Server`. Changes on a `Document` can be synchronized by using a `Client`.
- `Document`: A Document is a CRDT-based data type through which the model of the application is represented.
- `Server`: A `Server` receives changes from `Client`s, stores them in the DB, and propagates them to `Client`s who subscribe to `Document`s.

Next, we will look at how `Client` and `Document` described in Overview work when editing a `Document`.

Editing in Yorkie can be divided into Local Editing and Remote Editing.

Local Editing occurs on the machine where it is running. Conversely, Remote Editing occurs on another machine that is editing the `Document`.

### Local Editing

Local Editing is started by calling the `Document.Update`. `Document.Update` is usually called whenever edit occurs in the external editor.

![document-editing-upstream](media/document-editing-upstream.png)

The figure above explains the Document in more detail, and three internal components are added.

- `Root` represents the root of JSON object. This component is a `Proxy` and is used to create `Changes` when editing of the `Root` is executed.
- `CRDT` represents the real document(SOT, source of truth) and keep the consistency even if changes are applied asynchronously.
- `LocalChanges` is a buffer used to collect locally generated Changes before sending them to the server. The Client checks if changes are exist in LocalChanges and sends them to the server.

Local Editing consists of tree logic parts.

1. Calling `Document.Update`.
2. Pushing Changes to Server.
3. Propagating Changes to Peers.

Let's take a closer look at the logics.

#### 1. Calling `Document.Update`

This logic is executed with 3 sub-logics.

- 1-1. When the function is called, `Proxy` creates Changes.
- 1-2. And then the changes are added to LocalChanges.
- 1-3. The changes also apply to the `CRDT`. The `CRDT` is only updated by changes.

Here's a real code example:

```go
// Go SDK
doc.Update(func(root *json.Object) {
    root.SetString("foo", "bar")
})
fmt.Println(doc.Marshal()) // {"foo": "bar"}
```

The updater function, the first argument of `Document.Update`, provides the `root` as the first argument. The external editor executes the methods of the `root` to edit the Document. The `root` is a proxy, and whenever editing occurs, changes are created internally and the changes are pushed to `LocalChanges`. These logics work synchronously.

#### 2. Pushing Changes to Server

This logic is executed with 2 sub-logics.

- 2-1. `Client` checks `LocalChanges` of `Document` at specific intervals.
- 2-2. If there are changes in `LocalChanges`, `Client` sends them to the `Server`.

If LocalChanges has changes that need to be synchronized with other peers, it collects them and sends them to the server. These logics work asynchronously.

#### 3. Propagating Changes to Peers

The `Server` receives the changes from the `Client` and then stores the changes and propagates them to other `Client`s that are subscribing the `Document`.

### Remote Editing

Remote Editing starts when the server responds the changes to the client at the last part of Local Editing.

![document-editing-downstream](media/document-editing-downstream.png)

The `Client` that receives the changes applies the changes in the `CRDT` inside the `Document`. If there are handlers that are externally subscribed to changes through `Doc.Subscribe`, these handlers are called and the ChangeEvents are passed as an argument. All of these logic work synchronously.

```js
// JS SDK
doc.subscribe((event) => {
    console.log(event.type)
});
```

For more details: [Subscribing to Document events](https://yorkie.dev/docs/js-sdk#subscribing-to-document-events)

### Risks and Mitigation

Proxy can vary by language or environment. For example, in JS SDK, the Proxy is implemented as [JavaScript Proxy](https://developer.mozilla.org/en-US/docs/Web/JavaScript/Reference/Global_Objects/Proxy), but in the Go SDK, it is just a struct.
This is to provide users with an interface that suits the characteristics of the language or environment. Proxy component is likely to change to other interfaces later when we find a better way.
