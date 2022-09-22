---
title: retention
target-version: 0.2.17
---

# Retention

This document covers the implementation design of `backend-snapshot-with-purging-changes` flag.

## Summary

In a production environment, it is generally expected to support retention features. This allows users to set the range of data they need and saves storage by deleting data that they don't need. 

To support this feature, we need to implement various features, such as the scope of retention settings (server-level settings, user-level settings, document-level settings, etc.) and the amount or duration of data to be stored.
Currently as a starting point to support these features, the feature to delete changes at the server level is implemented by adding the `--backend-snapshot-with-purging-changes` flag.

This documentation is intended to explain how the `backend-snapshot-with-purging-changes` flag is implemented.

### Goals

Add the `--backend-snapshot-with-purging-changes` flag. If this flag is used, when creating a snapshot, unnecessary changes before the snapshot are deleted. This should not affect synchronization between clients.

### Non-Goals

Since the maximum number of changes that can be deleted are deleted, the number of remaining changes can not be predicted. And inevitably, only available history is output when using the `yorkie history` command. 

## Proposal Details

The --backend-snapshot-with-purging-changes flag was initially suggested in issue #288.
As you can see from the above issue, this is a retention-related option that delete synchronized changes when creating a snapshot.

### Background

The yorkie server basically saves all document changes under the name of change in the DB and delivers these changes to other clients to implement document synchronization.
In addition to this, in order to avoid the problem of synchronizing too many changes one by one, when the number of changes exceeds a certain number, a snapshot is created to record and synchronize the state of the document.

However, due to this structure, all changes of the document are stored in the DB, which has a problem of using a lot of storage space.
To improve this, the --backend-snapshot-with-purging-changes flag enables the feature to delete previous changes when a snapshot is created and the document state is recorded.

### How it works?

As you can see from the flag name, this feature deletes the changes before the snapshot when the snapshot is created, but more precisely, it deletes the changes that were synchronized before the snapshot.
This is to avoid the problem of not being synchronized because changes that have not yet been synchronized are deleted. 

To understand this, we need to understand how the server handles changes.


### Risks and Mitigation

The current implementation simply deletes the synchronized changes when the snapshot is created. A more detailed retention function needs to be added for future production environments.

### Future Plan
