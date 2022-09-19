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

This is where we detail how to use the feature with snippet or API and describe
the internal implementation.

### Risks and Mitigation

The current implementation simply deletes the synchronized changes when the snapshot is created. A more detailed retention function needs to be added for future production environments.