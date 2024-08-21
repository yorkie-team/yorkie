# Changelog

All notable changes to Yorkie will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and Yorkie adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.4.31] - 2024-08-21

### Added

- Add a metric to collect the number of background routines by @kokodak in https://github.com/yorkie-team/yorkie/pull/963

### Changed

- Modify health check endpoint and add HEAD method by @taeng0204 in https://github.com/yorkie-team/yorkie/pull/958
- [Revised] Fine-tuned CI Workflows in PR by @krapie in https://github.com/yorkie-team/yorkie/pull/965

### Fixed

- Fix invalid test case in FindDocInfosByKeys by @kokodak in https://github.com/yorkie-team/yorkie/pull/972

## [0.4.30] - 2024-08-09

### Added

- Add HTTP health check handler for server health monitoring by @taeng0204 in https://github.com/yorkie-team/yorkie/pull/952
- Show Server Version in Yorkie CLI by @hyun98 in https://github.com/yorkie-team/yorkie/pull/938

## [0.4.29] - 2024-08-05

### Added

- Support basic account action for admin by @gusah009 in https://github.com/yorkie-team/yorkie/pull/934

### Changed

- Update docker compose command to V2 by @fourjae in https://github.com/yorkie-team/yorkie/pull/950

### Fixed

- Fix FindDocInfosByKeys when keys is empty by @blurfx in https://github.com/yorkie-team/yorkie/pull/945
- Handle panic during conversion to connectCode by @blurfx in https://github.com/yorkie-team/yorkie/pull/951

## [0.4.28] - 2024-07-25

### Added

- Enhance housekeeping to add variety of tasks by @hackerwins in https://github.com/yorkie-team/yorkie/pull/932
- Enhance GetDocuments API by adding bulk retrieval by @kokodak in https://github.com/yorkie-team/yorkie/pull/931
- Improve performance for creating crdt.TreeNode by @m4ushold in https://github.com/yorkie-team/yorkie/pull/939

### Changed

- Update `updated_at` only when there are operations in changes by @window9u in https://github.com/yorkie-team/yorkie/pull/935

## [0.4.27] - 2024-07-11

### Changed

- Provide error codes for enhancing error handling from clients by @hackerwins in https://github.com/yorkie-team/yorkie/pull/927

### Fixed

- Prevent exposure of removed documents when searching by @hackerwins in https://github.com/yorkie-team/yorkie/pull/918
- Ensure proper deletion of documents in MemDB by @hackerwins in https://github.com/yorkie-team/yorkie/pull/920
- Handle local changes correctly when receiving snapshot by @raararaara in https://github.com/yorkie-team/yorkie/pull/923

## [0.4.26] - 2024-07-04

### Fixed

- Remove node from index during GC by @raararaara in https://github.com/yorkie-team/yorkie/pull/913

## [0.4.25] - 2024-07-03

### Added

- Add GetDocuments API returning document summaries by @hackerwins in https://github.com/yorkie-team/yorkie/pull/909

### Changed

- Update CI workflow to trigger Pull Request CI only on code-level changes by @kokodak in https://github.com/yorkie-team/yorkie/pull/906
- Return ErrAlreadyDetached when reattaching by @hackerwins in https://github.com/yorkie-team/yorkie/pull/908
- Add Project field to YorkieService logs by @hackerwins in https://github.com/yorkie-team/yorkie/pull/911

### Fixed

- Remove commit revision in version command by @hackerwins in https://github.com/yorkie-team/yorkie/pull/905
- Fix missing document detachments when client is deactivated by @raararaara in https://github.com/yorkie-team/yorkie/pull/907

## [0.4.24] - 2024-06-14

### Fixed

- Fix duplicate changes when syncing and detaching by @hackerwins in https://github.com/yorkie-team/yorkie/pull/896

## [0.4.23] - 2024-06-07

### Fixed

- Fix miscalculation of tree size in concurrent editing by @raararaara in https://github.com/yorkie-team/yorkie/pull/891

## [0.4.22] - 2024-06-04

## Added

- Add RHTNode removal to converter for consistency by @hackerwins in https://github.com/yorkie-team/yorkie/pull/888

## [0.4.21] - 2024-06-03

### Added

- Handle concurrent Tree.RemoveStyle by @hackerwins in https://github.com/yorkie-team/yorkie/pull/883

### Changed

- Return ErrClientNotActivated for deactivated clients on WatchDocument by @hackerwins in https://github.com/yorkie-team/yorkie/pull/877

### Fixed

- Fix incorrect tree snapshot encoding/decoding by @raararaara in https://github.com/yorkie-team/yorkie/pull/881

## [0.4.20] - 2024-05-24

### Added

- Implement RHT.GC by @hackerwins in https://github.com/yorkie-team/yorkie/pull/864
- Add Resource Configuration in `yorkie-mongodb` Helm chart by @krapie in https://github.com/yorkie-team/yorkie/pull/872
- Add snapshot-disable-gc flag by @hackerwins in https://github.com/yorkie-team/yorkie/pull/874

### Changed

- Move ToXML from RHT to TreeNode by @raararaara in https://github.com/yorkie-team/yorkie/pull/863
- Apply GCPair to TreeNode, TextNode by @hackerwins in https://github.com/yorkie-team/yorkie/pull/866

## [0.4.19] - 2024-05-10

### Fixed

- Handle concurrent editing and styling in Tree by @hackerwins in https://github.com/yorkie-team/yorkie/pull/854
- Fix inconsistent garbage collection for multiple nodes in text and tree type by @chacha912 in https://github.com/yorkie-team/yorkie/pull/855

## [0.4.18] - 2024-04-23

### Added

- Support `yorkie-monitoring` and `yorkie-argocd` Charts on NCP by @krapie in https://github.com/yorkie-team/yorkie/pull/846

## [0.4.17] - 2024-04-19

### Added

- Support NCP ALB by @hackerwins in https://github.com/yorkie-team/yorkie/pull/835

### Changed

- Move Client.Watch inside Client.Attach by @krapie in https://github.com/yorkie-team/yorkie/pull/803
- Use DBConnectionURI directly by @hackerwins in https://github.com/yorkie-team/yorkie/pull/833
- Move `istio-ingressgateway` to Yorkie Namespace by @krapie in https://github.com/yorkie-team/yorkie/pull/836

### Fixed

- Provide CODECOV_TOKEN to codecov-action by @hackerwins in https://github.com/yorkie-team/yorkie/pull/843

## [0.4.16] - 2024-03-29

### Fixed

- Fix incorrect calculation in `indexTree.treePosToPath` operation by @raararaara in https://github.com/yorkie-team/yorkie/pull/824
- Fix logic errors in TreeNode.DeepCopy by @raararaara in https://github.com/yorkie-team/yorkie/pull/821
- Fix missing escapeString in Tree Marshal by @chacha912 https://github.com/yorkie-team/yorkie/pull/830

## [0.4.15] - 2024-03-11

### Added

- Add Swagger Serving Command by @devleejb in https://github.com/yorkie-team/yorkie/pull/812
- Add MongoDB sharding document by @sejongk in https://github.com/yorkie-team/yorkie/pull/781
- Add merge and split concurrency tests by @justiceHui in https://github.com/yorkie-team/yorkie/pull/780

### Fixed

- Prevent RunTestConcurrency from creating garbage clients by @justiceHui in https://github.com/yorkie-team/yorkie/pull/793
- Add Test Server Wait Helper Function by @krapie in https://github.com/yorkie-team/yorkie/pull/787
- Update Design Document for Missing Document Link and Instructon by @krapie in https://github.com/yorkie-team/yorkie/pull/782

## [0.4.14] - 2024-01-29

### Added

- Introduce MongoDB sharding rules to Project-wide and Document-wide collections by @sejongk in https://github.com/yorkie-team/yorkie/pull/776
- Add Helm chart for MongoDB sharded cluster by @sejongk in https://github.com/yorkie-team/yorkie/pull/769
- Improve object creation with an initial value of specific types by @highcloud100 in https://github.com/yorkie-team/yorkie/pull/766

### Changed

- Refactor concurrency tests for basic Tree.Edit by @justiceHui in https://github.com/yorkie-team/yorkie/pull/772
- Remove unnecessary `String()` calls with `stringer` interface by @sejongk in https://github.com/yorkie-team/yorkie/pull/777

## [0.4.13] - 2024-01-19

### Added

- Add API for Retrieving All Documents by @raararaara in https://github.com/yorkie-team/yorkie/pull/755
- Introduce object creation interface with initial values by @highcloud100 in https://github.com/yorkie-team/yorkie/pull/756
- Implement Tree.RemoveStyle by @justiceHui in https://github.com/yorkie-team/yorkie/pull/748

### Fixed

- Fix RHT.Remove and Add test code by @justiceHui in https://github.com/yorkie-team/yorkie/pull/752
- FIx Finding Housekeeping Candidates and Modify Housekeeping Structure by @devleejb in https://github.com/yorkie-team/yorkie/pull/749
- Add concurrent editing test cases in Tree by @sejongk in https://github.com/yorkie-team/yorkie/pull/751

## [0.4.12] - 2024-01-05

### Changed

- Introduce TreeToken and tokensBetween to Tree by @sejongk in https://github.com/yorkie-team/yorkie/pull/747
- Add RPC and profiling ports to Yorkie deployment by @hackerwins in https://github.com/yorkie-team/yorkie/pull/727

### Fixed

- Change CLI TLS configuration to HTTP2 by @krapie in https://github.com/yorkie-team/yorkie/pull/742
- Replace grpcstatus.Errorf with connect.Error by @chacha912, @highcloud100 in https://github.com/yorkie-team/yorkie/pull/741
- Fix `getGarbageLen` to retrun correct size by @devleejb in https://github.com/yorkie-team/yorkie/pull/743
- Fix typo in `MAINTAINING.md` by @devleejb in https://github.com/yorkie-team/yorkie/pull/739

## [0.4.11] - 2023-12-18

### Added

- Support concurrent insertion and splitting in Tree by @sejongk in https://github.com/yorkie-team/yorkie/pull/725

### Changed

- Migrate RPC to ConnectRPC by @krapie, @hackerwins https://github.com/yorkie-team/yorkie/pull/703

### Fixed

- Address duplicate nodeIDs in Tree.Split @sejongk https://github.com/yorkie-team/yorkie/pull/724

## [0.4.10] - 2023-12-04

### Added

- Add Rate Limit using Istio Envoy by @joonhyukchoi in https://github.com/yorkie-team/yorkie/pull/674
- Implement splitLevel of Tree.Edit by @hackerwins in https://github.com/yorkie-team/yorkie/pull/705

### Changed

- Bump github.com/spf13/viper from 1.15.0 to 1.17.0 by @dependabot in https://github.com/yorkie-team/yorkie/pull/693
- Bump github.com/jedib0t/go-pretty/v6 from 6.4.0 to 6.4.9 by @dependabot in https://github.com/yorkie-team/yorkie/pull/695
- Bump actions/setup-go from 3 to 4 by @dependabot in https://github.com/yorkie-team/yorkie/pull/698
- Bump helm/chart-releaser-action from 1.5.0 to 1.6.0 by @dependabot in https://github.com/yorkie-team/yorkie/pull/699
- Bump docker/login-action from 2 to 3 by @dependabot in https://github.com/yorkie-team/yorkie/pull/700
- Bump google.golang.org/grpc from 1.58.2 to 1.58.3 by @dependabot in https://github.com/yorkie-team/yorkie/pull/701
- Bump golang.org/x/crypto from 0.14.0 to 0.16.0 by @dependabot in https://github.com/yorkie-team/yorkie/pull/702
- Bump github.com/rs/xid from 1.4.0 to 1.5.0 by @dependabot in https://github.com/yorkie-team/yorkie/pull/697

## [0.4.9] - 2023-11-25

### Added

- Implement merge elements in Tree.Edit by @hackerwins in https://github.com/yorkie-team/yorkie/pull/659
- Add PushPull benchmark test by @sejongk in https://github.com/yorkie-team/yorkie/pull/658
- Setup Dependabot by @jongwooo in https://github.com/yorkie-team/yorkie/pull/675

### Changed

- Bump up golangci-lint and fix lint errors by @hackerwins in https://github.com/yorkie-team/yorkie/pull/660
- Modify converter to allow setting values for object and array by @chacha912 and @hackerwins in https://github.com/yorkie-team/yorkie/pull/687

## [0.4.8] - 2023-11-01

### Changed

- Bump golang.org/x/net from 0.10.0 to 0.17.0 by @dependabot in https://github.com/yorkie-team/yorkie/pull/649
- Bump google.golang.org/grpc from 1.54.0 to 1.56.3 by @dependabot in https://github.com/yorkie-team/yorkie/pull/654
- Fix ArgoCD version to v2.7.10 by @krapie in https://github.com/yorkie-team/yorkie/pull/653
- Rename StructureAsString to toTestString by @hackerwins in https://github.com/yorkie-team/yorkie/pull/656

### Fixed

- Revise Prometheus PVC Spec Syntax Error by @krapie in https://github.com/yorkie-team/yorkie/pull/650
- Remove skip storing MinSyncedTicket when the ticket is initial by @hackerwins in https://github.com/yorkie-team/yorkie/pull/655

## [0.4.7] - 2023-10-06

### Added

- Introduce Broadcast API by @sejongk in https://github.com/yorkie-team/yorkie/pull/631
- Add context to CLI for simplifying CLI commands by @Wu22e and @hackerwins in https://github.com/yorkie-team/yorkie/pull/647
- Add Tree.Edit benchmark by @JOOHOJANG in https://github.com/yorkie-team/yorkie/pull/637

### Changed

- Bump checkout from v3 to v4 by @Yaminyam in https://github.com/yorkie-team/yorkie/pull/641
- Remove panic from crdt.Primitive @fourjae and @hackerwins in https://github.com/yorkie-team/yorkie/pull/636

### Removed

- Remove unused trie by @hackerwins in https://github.com/yorkie-team/yorkie/pull/646

### Fixed

- Support concurrent formatting of Text by @MoonGyu1 in https://github.com/yorkie-team/yorkie/pull/639
- Fix typo in retention design document by @LakHyeonKim in https://github.com/yorkie-team/yorkie/pull/633
- Update Design Document for Missing Document Links and Ordering by @krapie in https://github.com/yorkie-team/yorkie/pull/630
- Recover Select to prevent unsupported operation by @hackerwins in https://github.com/yorkie-team/yorkie/pull/629

## [0.4.6] - 2023-08-25

### Added

- Set cobra default output to stdout by @blurfx in https://github.com/yorkie-team/yorkie/pull/599
- Fetch latest snapshot metadata to determine snapshot creation need by @hyemmie in https://github.com/yorkie-team/yorkie/pull/597
- Update contributing docs by @MoonGyu1 in https://github.com/yorkie-team/yorkie/pull/601
- Add Pagination to Listing Projects for Housekeeping by @tedkimdev in https://github.com/yorkie-team/yorkie/pull/587
- Update workflow with latest versions of the actions which runs on Node16 by @jongwooo in https://github.com/yorkie-team/yorkie/pull/620
- Add integration tree test for sync with js-sdk by @MoonGyu1 in https://github.com/yorkie-team/yorkie/pull/611
- Add testcases for sync with js-sdk by @MoonGyu1 in https://github.com/yorkie-team/yorkie/pull/621
- Add tree document by @MoonGyu1 in https://github.com/yorkie-team/yorkie/pull/608
- Cache ProjectInfo by @blurfx in https://github.com/yorkie-team/yorkie/pull/586
- Handle concurrent editing of Tree.Edit by @hackerwins, @MoonGyu1, @sejongk in https://github.com/yorkie-team/yorkie/pull/607
- Support multi-level and partial element selection by @sejongk, @hackerwins in https://github.com/yorkie-team/yorkie/pull/624

### Changed

- Remove Select operation from Text by @joonhyukchoi in https://github.com/yorkie-team/yorkie/pull/589
- Change 'Documents' from plural to singular in DocEvent by @chacha912 in https://github.com/yorkie-team/yorkie/pull/613
- Cleanup proto by @chacha912 in https://github.com/yorkie-team/yorkie/pull/614
- Replace matrix strategy with environment variable by @jongwooo in https://github.com/yorkie-team/yorkie/pull/619
- Change TreeNode to have IDs instead of insPrev, insNext by @JOOHOJANG in https://github.com/yorkie-team/yorkie/pull/622

### Fixed

- Fix typos and invalid link in the yorkie design document by @g2hhh2ee in https://github.com/yorkie-team/yorkie/pull/591
- Clean up code by @hackerwins in https://github.com/yorkie-team/yorkie/pull/595
- Clean up methods related to presence by @chacha912 in https://github.com/yorkie-team/yorkie/pull/594
- Remove panic from crdt.RGATreeList by @sejongk in https://github.com/yorkie-team/yorkie/pull/596
- Fix treePos calculating logic for text node by @JOOHOJANG in https://github.com/yorkie-team/yorkie/pull/615

## [0.4.5] - 2023-07-20

### Added

- Move Presence from Client to Document by @chacha912, @hackerwins in https://github.com/yorkie-team/yorkie/pull/582
- Add include-snapshot flag to ListDocuments API by @YoonKiJin, @hackerwins in https://github.com/yorkie-team/yorkie/pull/575

### Changed

- Revise log messages by @hackerwins in https://github.com/yorkie-team/yorkie/pull/574
- Bump google.golang.org/grpc from 1.50.0 to 1.53.0 by @dependabot in https://github.com/yorkie-team/yorkie/pull/576
- Allow users to pass multi nodes when calling Tree.edit by @JOOHOJANG in https://github.com/yorkie-team/yorkie/pull/579

### Fixed

- Remove unnecessary codes from gc by @JOOHOJANG in https://github.com/yorkie-team/yorkie/pull/581

## [0.4.4] - 2023-07-04

### Added

- Add logout command to CLI by @blurfx in https://github.com/yorkie-team/yorkie/pull/571
- Add RemoveIfNotAttached flag to Detach by @emplam27 in https://github.com/yorkie-team/yorkie/pull/560

### Fixed

- Make attributes display properly in dashboard by @YoonKiJin in https://github.com/yorkie-team/yorkie/pull/564
- Remove panic from crdt.Tree and index.Tree by @JOOHOJANG in https://github.com/yorkie-team/yorkie/pull/570

## [0.4.3] - 2023-06-29

### Added

- Add force flag to RemoveDocument command by @emplam27 in https://github.com/yorkie-team/yorkie/pull/558
- Apply garbage collection for tree by @JOOHOJANG in https://github.com/yorkie-team/yorkie/pull/566

### Fixed

- Resolve CI failure with longer MaxConnectionAge by @krapie in https://github.com/yorkie-team/yorkie/pull/556
- Update ClientInfo with ID and extract `testcases` package by @emplam27 in https://github.com/yorkie-team/yorkie/pull/557
- Filter out removed documents from ListDocuments API by @hackerwins in https://github.com/yorkie-team/yorkie/pull/563
- Add a workaround to prevent retrieving removed documents from MemDB by @hackerwins in https://github.com/yorkie-team/yorkie/pull/565

## [0.4.2] - 2023-06-19

### Added

- Add TLS Option & Insecure Flag in Admin CLI by @krapie in https://github.com/yorkie-team/yorkie/pull/548
- Implement Tree.Style for setting attributes to nodes by @krapie, @hackerwins in https://github.com/yorkie-team/yorkie/pull/549

### Changed

- Abstract the workflow to base-docker-publish.yml by @jongwooo in https://github.com/yorkie-team/yorkie/pull/552
- Change k8s version of yorkie-cluster chart to 1.23 by @emplam27 in https://github.com/yorkie-team/yorkie/pull/550

## [0.4.1] - 2023-06-09

### Fixed

- Support UTF16 Code Units in yorkie.Tree by @krapie in https://github.com/yorkie-team/yorkie/pull/545

## [0.4.0] - 2023-06-05

### Added

- Implement yorkie.Tree for text editors using tree model by @hackerwins in https://github.com/yorkie-team/yorkie/pull/535
- Add remove document command to CLI by @emplam27 in https://github.com/yorkie-team/yorkie/pull/540

### Fixed

- Remove panic method in crdt array by @emplam27 in https://github.com/yorkie-team/yorkie/pull/524
- Revise Helm Charts for Production Installations by @krapie in https://github.com/yorkie-team/yorkie/pull/537
- Resolve concurrent map issue by @chacha912 in https://github.com/yorkie-team/yorkie/pull/538

## [0.3.5] - 2023-05-22

### Added

- Add Sharded Cluster Mode Design Document by @krapie in https://github.com/yorkie-team/yorkie/pull/523

### Changed

- Remove panic and wrapping informational error from server by @emplam27 in https://github.com/yorkie-team/yorkie/pull/519
- Remove panic method in crdt text by @emplam27 in https://github.com/yorkie-team/yorkie/pull/522
- Integrate SDK RPC Server and Admin RPC Server to Single RPC Server by @krapie in https://github.com/yorkie-team/yorkie/pull/532

### Fixed

- Update Helm Chart Releaser Action by @krapie in https://github.com/yorkie-team/yorkie/pull/516
- Revise Helm charts & README.md by @krapie in https://github.com/yorkie-team/yorkie/pull/525
- Resolve Helm Chart Installation Fail on Custom Release Name by @krapie in https://github.com/yorkie-team/yorkie/pull/531

## [0.3.4] - 2023-04-18

### Added

- Add Yorkie Helm Charts by @krapie in https://github.com/yorkie-team/yorkie/pull/507
- Add gRPC MaxConnectionAge & MaxConnectionAgeGrace Options by @krapie in https://github.com/yorkie-team/yorkie/pull/512
- Extend PushPull to support sync mode by adding push-only flag by @humdrum in https://github.com/yorkie-team/yorkie/pull/500

### Removed

- Remove etcd-based cluster mode and replace it with sharding by @hackerwins in https://github.com/yorkie-team/yorkie/pull/504

### Fixed

- Lock watchDocuments depending on the client and doc by @chacha912 in https://github.com/yorkie-team/yorkie/pull/506
- Fixed a guide about path of docker-compose.xml file by @maruldy in https://github.com/yorkie-team/yorkie/pull/511

## [0.3.3] - 2023-03-24

### Added

- Add ClientDeactivateThreshold field in admin CLI project list by @krapie in https://github.com/yorkie-team/yorkie/pull/477
- Add RemoveDocument API by @hackerwins in https://github.com/yorkie-team/yorkie/pull/484
- Add user agent metrics by @emplam27 in https://github.com/yorkie-team/yorkie/pull/492
- Add shard key in context by @hackerwins in https://github.com/yorkie-team/yorkie/pull/499
- Add pagination flags to document ls command by @emplam27 in https://github.com/yorkie-team/yorkie/pull/489

### Changed

- Allow uppercase letters(A-Z) for document keys by @shiningsoo in https://github.com/yorkie-team/yorkie/pull/483
- Bump golang.org/x/net from 0.0.0-20221004154528-8021a29435af to 0.7.0 by @dependabot in https://github.com/yorkie-team/yorkie/pull/486
- Change the structure of WatchDocument API by @chacha912 in https://github.com/yorkie-team/yorkie/pull/491

### Fixed

## [0.3.1] - 2023-02-27

### Added

- Add ClientDeactivateThreshold in Project by @krapie in https://github.com/yorkie-team/yorkie/pull/454
- Add validation module and document key validation by @easylogic in https://github.com/yorkie-team/yorkie/pull/467

### Changed

- Filter out unsubscribed documents key in DocEvent by @chacha912 in https://github.com/yorkie-team/yorkie/pull/463
- Remove priority queue from RHTPQMap and entire project by @blurfx in https://github.com/yorkie-team/yorkie/pull/462

### Fixed

- Remove duplicated backslash in string escaping by @cozitive in https://github.com/yorkie-team/yorkie/pull/458
- Fix invalid index of SplayTree with single node by @hackerwins in https://github.com/yorkie-team/yorkie/pull/470

## [0.3.0] - 2023-01-31

### Changed

- Merge Text and RichText by @hackerwins in https://github.com/yorkie-team/yorkie/pull/438
- Fix the value type of Counter and remove double type from Counter by @cozitive in https://github.com/yorkie-team/yorkie/pull/441

### Fixed

- Fix wrong string escape in Text's attrs by @cozitive in https://github.com/yorkie-team/yorkie/pull/443
- Increase CRDT Counter in local change by @cozitive in https://github.com/yorkie-team/yorkie/pull/449

## [0.2.20] - 2022-12-30

### Changed

- Bump up Go to 1.19.2 by @hackerwins in https://github.com/yorkie-team/yorkie/pull/425
- Bump up libraries to the latest version by @hackerwins in https://github.com/yorkie-team/yorkie/pull/426
- Remove use of bou.ke/monkey library by @chromato99 in https://github.com/yorkie-team/yorkie/pull/427
- Replace deprecated ioutil library by @chromato99 in https://github.com/yorkie-team/yorkie/pull/428
- Remove duplicate logging when the function returns error by @hackerwins in https://github.com/yorkie-team/yorkie/pull/429

### Fixed

- Fix typo by @ppeeou in https://github.com/yorkie-team/yorkie/pull/421
- Fix invalid JSON from marshaling dates and use UNIX ms for Date by @hackerwins in https://github.com/yorkie-team/yorkie/pull/432
- Add additional unwrap code in ToStatusError gRPC error handler by @Krapi0314 in https://github.com/yorkie-team/yorkie/pull/434

## [0.2.19] - 2022-10-04

### Added

- Add signup validation: #407

### Fixed

- Remove unused nodeMapByCreatedAt in RHT: #408
- Remove size cache from RGATreeList and use SplayTree instead: #415
- Adjust indexes so that each user has separate project names: #418

## [0.2.18] - 2022-09-05

### Added

- Bind project and user with owner field: #398
- Validate fields when creating or updating a project: #399

## [0.2.17] - 2022-08-25

### Added

- Add `--backend-snapshot-with-purging-changes` flag: #370

### Fixed

- Fix history command authentication error: #397

## [0.2.16] - 2022-08-16

### Added

- Add `--auth-webhook-url`, `--name` flag to updateProject command: #376

### Changed

- Rename package names to match JS SDK: #395

## [0.2.15] - 2022-08-08

### Added

- Introduce `buf` to enforce lint rules designed to guarantee consistency: #382
- Extract admin settings with flags and configurations: #384

### Changed

- Change uint64 to int64 among data inserted into the DB: #381
- Add `[jstype = JS_STRING]` field option in admin.proto: #380

## [0.2.14] - 2022-08-03

### Added

- Add signup and login commands and APIs: #357

### Fixed

- Fix the problem local changes were applied twice: #375

## [0.2.13] - 2022-07-27

### Added

- Add document list command to CLI: #366
- Add SearchDocuments admin API: #363

## [0.2.12] - 2022-07-20

### Fixed

- Fix incorrect index for nodes newly created then concurrently removed: #364

## [0.2.11] - 2022-07-14

### Added

- Apply gRPC error details to update project api: #354
- Implement pagination flags to history command: #360

## [0.2.10] - 2022-07-06

### Added

- Add MaxCallRecvMsgSize option to client: #353

### Changed

- Improve performance of deletion in Text: #356

### Fixed

- Fix a bug when deleting blocks concurrently: #b645cf1

## [0.2.9] - 2022-06-30

### Added

- Add history command to CLI: #349
- Introduce validator for project name: #345

### Fixed

- Revert text delection improvements: #350

## [0.2.8] - 2022-06-22

### Added

- Add UpdateProject admin API: #338

### Changed

- Improve performance of deletion in Text: #341

## [0.2.7] - 2022-06-14

### Fixed

- Expose the missing exit code: #e87d9d3
- Skip storing the initial ticket to prevent GC interruption: #339
- Cache removed elements when creating a document from a snapshot: #340
- Detach the attached documents when deactivating the client: #342

## [0.2.6] - 2022-05-25

### Changed

- Update Go version to 1.18: #326
- Add trylock to memory locker: #234
- Print projects in table format: #324
- Fetch documents by project: #330
- Add time attributes to document API: #325

### Fixed

- Fix invalid JSON returns from document.Marshal: #328, #332

## [0.2.5] - 2022-05-11

### Added

- Add the missing admin-port flag for CLI

### Fixed

- Rename projectID written in invalid conventions to project_id

## [0.2.4] - 2022-05-10

### Added

- Add Project(Multi-tenancy): #313, #319
- Add Admin API(ListDocuments, GetDocument, ListProjects): #309, #312, #315, #320

### Changed

- Cache ActorID.String to reduce memory usage: #308
- Rename Agent to Server: #311
- Rename Client Metadata to Presence: #323

### Fixed

- Fix LRU cache expiration when updating same key: #314

### Removed

- Remove collection from document: #318

## [0.2.3] - 2022-04-07

### Changed

- Introduce named logging to separate logs by request or routine: #296
- Add missing serverSeq of change.ID in Protobuf: #f5a0c49
- Cache the key of RGATreeSplitNodeID to prevent instantiation: #306
- Cache the key of TimeTicket to prevent instantiation: #307

### Fixed

- Fix for use on Windows: #295
- Fix snapshot interval to make them trigger properly in memdb: #297
- Run tests using monkey patch only on amd64: #299
- Fix a warning that directory does not exist when running make proto: #c441b7b

## [0.2.2] - 2022-01-24

### Added

- Add log-level flag: #290

### Fixed

- Fix a bug that reads config file incorrectly: #4cf184d
- Calculate minSyncedTicket based on time.Ticket: #289

## [0.2.1] - 2022-01-04

### Added

- Allow users to set up logger of the client: #285
- Housekeeping to deactivate clients that have not been updated: #286
- Run GC when saving snapshots: #287

### Changed

- Clean up client options: #284

2nd year release

## [0.2.0] - 2021-12-19

2nd year release

### Added

- Monitoring #155
- Supporting TLS and Auth webhook #6
- Providing Cluster Mode #11
- Improved Peer Awareness #153
- Providing MemoryDB for Agent without MongoDB #276

### Changed

### Removed

### Deprecated

## [0.1.12] - 2021-12-04

### Fixed

- Fix a bug to pull changes from other documents in MemDB: #9c2af2e

## [0.1.11] - 2021-12-04

### Added

- Add MemDB to run Yorkie without MongoDB: #276
- Add rpc-max-requests-bytes flag to set client request limit: #e544cdb

### Changed

- Avoid creating snapshots of a document at the same time in ETCD: #274
- Extract auth-webhook-cache-size as config and flag: #3256b95

### Fixed

- Fix a bug where text nodes with tombstones were not counted: #277

## [0.1.10] - 2021-11-16

### Added

- Add enable-pprof flag to open pprof via profiling server: #265
- Build multiple architecture docker images: #10d8c8b
- Add operations metrics in PushPull API: #d23fc14

### Changed

- Replace XXXGauges with XXXCounters: #266
- Reduce memory usage in PushPull API: #268

### Fixed

- Fix goroutine leaks on subscriptions: #265
- Add missing go process collector on metrics: #35cefdb
- Fix missing gRPC interceptors for metrics: #901e4fa

## [0.1.9] - 2021-10-31

### Added

- Add ETCD username and password flags: #259

### Changed

- Change the flag missed when renaming to AuthWebHookXXX: #feb831d

### Fixed

- Fix a bug that gRPC metrics were not displayed: #02c1995
- Fix a bug that Go process metrics were not displayed: #262

## [0.1.8] - 2021-10-21

### Fixed

- Revert "Replace hex.EncodeTostring with ActorID.key in Text and RichText (#255)"

## [0.1.7] - 2021-10-19

### Added

- Improve Client's metadata to be updatable: #153
- Build binaries for environments when releasing a new version: #175

### Changed

- Add config validation: #206
- Clean up flags in AuthXXX and XXXSec patterns: #168
- Update MaxConcurrentStreams to max: #227
- Clear performance bottlenecks: #251
- Change config format to YAML: #223

### Fixed

- Fix reduce array size when deleting the same position: #235
- Fix invalid version package path: #241
- Add registry missing in PR 185 to Metrics: #e65d5bb

## [0.1.6] - 2021-07-24

### Added

- Add Cluster Mode: #183
- Add Authorization check to Watch API: #209
- Add authorization-webhook-methods flag: #193
- Add retry logic to Authorization webhook: #194
- Add authorization webhook cache: #192

### Changed

- Change Watch events to be similar to JS SDK: #137
- Close Watch streams on agent shutdown: #208

### Fixed

- Fix a bug where deleted values from objects are revivded after GC: #202

## [0.1.5] - 2021-06-05

### Added

- Add basic behavior of authorization webhook: #188

### Fixed

- Fix the concurrent editing issue of Move Operation: #196

### Removed

- Delete RequestHeader in Protobuf: #188

## [0.1.4] - 2021-05-15

### Added

- Add gRPC health checking: #176
- Add ca-certificates to access remote DBs such as MongoDB Atlas: #6d3e176

### Changed

- Expose only basic command in Dockerfile: #317320b

### Fixed

- Fix incorrect sequences when detaching documents: #173

## [0.1.3] - 2021-04-04

### Added

- Add more metrics related to PushPull API: #166
- Add command-line flags for agent command: #167
- Support for null values: #160

### Changed

- Update Go version to 1.16: #161
- Calculate the size of Text in UTF-16 code units: #165

### Fixed

- Fix invalid states of SplayTree: #162
- Remove errors that occur when insPrev does not exist: #164

## [0.1.2] - 2021-02-14

### Added

- Add customizable metadata for peer awareness: #138

### Changed

- Use Xid instead of UUID for SubscriptionID: #142
- Replace the type of client_id to a byte array to reduce payload: #145

### Fixed

- Fix actorID loss while converting Change to ChangeInfo: #144

## [0.1.1] - 2021-01-01

### Added

- Add version tag when pushing image: #102
- Add garbage collection for TextElement: #104

### Changed

- Use multi-stage build for docker image: #107
- Wrap additional information to sentinel errors: #129
- Use status for one-dimensional value: #131
- Remove panics in the converter: #132, #135

### Fixed

- Fix check already attached document: #111
- Delete removed elements from the clone while running GC: #103
- Fix to use internal document when pulling snapshot from agent: #120

## [0.1.0] - 2020-11-07

First public release

### Added

- Add basic structure of Yorkie such as `Document`, `Client`, and Agent(`yorkie`)
- Add Custom CRDT data type `Text` for code editor: #2
- Add Snapshot API to reduce payload: #9
- Add Garbage Collection to clean CRDT meta: #3
- Add Custom CRDT data type `RichText` for WYSIWYG editor: #7
- Add Peer Awareness API: #48
- Add Prometheus metrics: #76
- Add Custom CRDT data type `Counter`: #82
