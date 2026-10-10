//go:build integration

/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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

// This file provides the deterministic text workload generator and the
// seeding helper used by the snapshot-gap benchmark
// (snapshot_cache_sizegap_bench_test.go).
//
// Why: issue #1957 asks whether the default SnapshotThreshold (500) is a
// good choice for PushPull. Answering it needs documents of a controlled
// size that sit a controlled number of changes behind the server. This file
// generates such documents and seeds them into the database; the benchmark
// itself places the stored snapshot. It makes no recommendation for the
// default value.
//
// Requirements: everything here is behind the integration build tag.
// TestSeedDocumentAtGap and TestWorkloadMatrix_SizeAndGap need a running
// MongoDB (see helper.TestMongoConfig, localhost:27017 by default) and use
// the package-level testBackend and testClient defined in another file of
// package packs_test. TestGenerateTextWorkload_ConvergesToTargetSize and
// TestRandomContent_WordDiversity need no database. Run them with:
//
//	go test ./server/packs -tags integration -count=1 \
//	  -run 'TestSeedDocumentAtGap|TestWorkloadMatrix_SizeAndGap|TestGenerateTextWorkload|TestRandomContent'
//
// Reproducibility: the seed, the 40 bytes-per-change ratio, DeleteRatio, the
// word corpus and the sentence templates determine the generated text and
// therefore every wire size reported by the benchmark. Do not change them
// without rerunning and updating the recorded results.

package packs_test

import (
	"context"
	stdjson "encoding/json"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

// Config controls a generated text workload
type Config struct {
	// TargetBytes is the target document size in bytes
	TargetBytes int

	// TargetChanges is the number of Update calls to generate
	TargetChanges int

	// DeleteRatio controls how often delete or replace is chosen.
	// While the remaining size shortfall averages at least one byte per
	// remaining edit, a "delete" roll becomes a Replace, so pure deletes
	// only occur once the document is close to or above the target size.
	DeleteRatio float64

	// Seed makes the workload deterministic
	Seed int64
}

// DefaultConfig returns a workload config for the given target size.
//
// 40 bytes of net growth per change is an empirical ratio that keeps the
// number of changes proportional to the document size. The same ratio is
// used in seedDocumentAtGap and the matrix tests.
func DefaultConfig(targetBytes int, seed int64) Config {
	targetChanges := max(targetBytes/40, 1)

	return Config{
		TargetBytes:   targetBytes,
		TargetChanges: targetChanges,
		DeleteRatio:   0.2,
		Seed:          seed,
	}
}

// EditKind is the type of a text edit
type EditKind int

const (
	KindInsert EditKind = iota
	KindDelete
	KindReplace
)

func (k EditKind) String() string {
	switch k {
	case KindInsert:
		return "Insert"
	case KindDelete:
		return "Delete"
	case KindReplace:
		return "Replace"
	default:
		return "Unknown"
	}
}

// TextEdit describes one Text.Edit call
type TextEdit struct {
	Kind    EditKind
	From    int
	To      int
	Content string
}

// String returns a short form of the edit
func (e TextEdit) String() string {
	c := []rune(e.Content)
	if len(c) > 20 {
		c = append(append([]rune{}, c[:20]...), []rune("...")...)
	}

	return fmt.Sprintf("%s(%d, %d, %q)", e.Kind, e.From, e.To, string(c))
}

// wordCorpus is the set of words used to generate text with different
// content. It mixes ASCII and Hangul on purpose: Hangul is multi-byte in
// UTF-8 while text offsets are counted in runes, so the workload exercises
// both. Sizes in Config are bytes, edit positions are runes.
var wordCorpus = []string{
	// English — general
	"the", "quick", "brown", "fox", "jumps", "over", "lazy", "dog",
	"team", "project", "plan", "meeting", "schedule", "deadline",
	"priority", "issue", "ticket", "sprint", "backlog", "review",

	// English — systems/CRDT/networking
	"document", "change", "server", "client", "yorkie", "crdt",
	"sync", "commit", "branch", "merge", "conflict", "resolve",
	"cache", "queue", "thread", "socket", "packet", "token",
	"session", "request", "response", "payload", "schema", "index",
	"query", "record", "batch", "stream", "buffer", "snapshot",
	"checkpoint", "threshold", "latency", "throughput", "replica",
	"cluster", "node", "shard", "partition", "backup", "restore",
	"migrate", "validate", "authenticate", "authorize", "encrypt",
	"decrypt", "compress", "parse", "render", "compile", "deploy",
	"monitor", "alert", "log", "metric", "trace", "span", "event",
	"handler", "listener", "callback", "async", "module", "package",
	"import", "export", "interface", "struct", "pointer", "reference",
	"allocate", "garbage", "collect", "version", "vector", "lamport",

	// Korean — general
	"사용자", "프로젝트", "회의", "일정", "요구사항", "구현", "테스트",
	"배포", "성능", "동기화", "데이터", "변경사항", "문서", "서버",
	"클라이언트", "협업", "저장", "편집", "확인", "리뷰", "마감",
	"우선순위", "버그", "이슈", "티켓", "스프린트", "일정표", "배치작업",

	// Korean — systems/technical
	"머지", "브랜치", "커밋", "충돌", "캐시", "큐", "스레드", "소켓",
	"세션", "요청", "응답", "스키마", "인덱스", "쿼리", "레코드",
	"스트림", "버퍼", "스냅샷", "체크포인트", "임계값", "지연시간",
	"처리량", "복제본", "클러스터", "노드", "샤드", "파티션", "백업",
	"복원", "마이그레이션", "검증", "인증", "권한", "암호화", "복호화",
	"압축", "파싱", "렌더링", "컴파일", "모니터링", "알림", "로그",
	"지표", "추적", "이벤트", "핸들러", "리스너", "콜백", "비동기",
	"모듈", "패키지", "인터페이스", "구조체", "포인터", "참조", "할당",
	"가비지", "수집", "알고리즘", "자료구조", "네트워크", "프로토콜",
	"트랜잭션", "락", "동시성", "병렬처리",
}

// Sentence patterns used by randomContent
var sentenceTemplates = []string{
	"%s가 %s를 수정했습니다.",
	"서버에서 새로운 %s을 확인했습니다.",
	"여러 %s가 동시에 %s를 편집했습니다.",
	"%s 내용은 다음 %s 과정에서 반영됩니다.",
	"%s와 %s 간의 %s가 발생했습니다.",
	"이번 %s에서 %s 관련 %s를 처리했습니다.",
	"%s 팀은 %s 일정을 %s로 조정했습니다.",
	"The %s was updated by the %s.",
	"A new %s was pushed to the %s.",
	"Multiple %s modified the %s concurrently.",
	"The %s will be reflected during the next %s.",
	"We resolved a %s between %s and %s.",
	"This %s depends on the %s from the previous %s.",
}

func randomSentence(rng *rand.Rand) string {
	tmpl := sentenceTemplates[rng.Intn(len(sentenceTemplates))]
	n := strings.Count(tmpl, "%s")

	args := make([]any, n)
	for i := range n {
		args[i] = wordCorpus[rng.Intn(len(wordCorpus))]
	}

	return fmt.Sprintf(tmpl, args...)
}

// GenerateTextWorkload generates edits against an in-memory text buffer
func GenerateTextWorkload(cfg Config) ([]TextEdit, error) {
	if cfg.TargetBytes < 0 {
		return nil, fmt.Errorf(
			"workload: TargetBytes must be >= 0, got %d",
			cfg.TargetBytes,
		)
	}

	if cfg.TargetChanges <= 0 {
		return nil, fmt.Errorf(
			"workload: TargetChanges must be positive, got %d",
			cfg.TargetChanges,
		)
	}

	if cfg.DeleteRatio < 0 || cfg.DeleteRatio >= 1 {
		return nil, fmt.Errorf(
			"workload: DeleteRatio must be in [0,1), got %f",
			cfg.DeleteRatio,
		)
	}

	rng := rand.New(rand.NewSource(cfg.Seed)) //nolint:gosec // seeded for reproducibility
	virtual := make([]rune, 0, cfg.TargetBytes)
	currentBytes := 0

	ops := make([]TextEdit, 0, cfg.TargetChanges)

	for i := 0; i < cfg.TargetChanges; i++ {
		remainingOps := cfg.TargetChanges - i
		avgBytesPerRemainingOp :=
			(cfg.TargetBytes - currentBytes) / remainingOps

		edit := planEdit(
			rng,
			virtual,
			cfg.DeleteRatio,
			avgBytesPerRemainingOp,
		)

		removedBytes := len(string(virtual[edit.From:edit.To]))
		currentBytes += len(edit.Content) - removedBytes

		next := make(
			[]rune,
			0,
			len(virtual)-
				(edit.To-edit.From)+
				len([]rune(edit.Content)),
		)

		next = append(next, virtual[:edit.From]...)
		next = append(next, []rune(edit.Content)...)
		next = append(next, virtual[edit.To:]...)
		virtual = next

		ops = append(ops, edit)
	}

	return ops, nil
}

// planEdit chooses the edit type, position, and content
func planEdit(
	rng *rand.Rand,
	virtual []rune,
	deleteRatio float64,
	avgBytesPerRemainingOp int,
) TextEdit {
	n := len(virtual)
	behindTarget := avgBytesPerRemainingOp > 0

	if n == 0 {
		content := randomContent(
			rng,
			growthChunk(rng, avgBytesPerRemainingOp),
		)

		return TextEdit{
			Kind:    KindInsert,
			From:    0,
			To:      0,
			Content: content,
		}
	}

	roll := rng.Float64()

	if roll < deleteRatio {
		from, to := randomRange(rng, n, deleteSpan(rng))

		if behindTarget {
			content := randomContent(
				rng,
				growthChunk(rng, avgBytesPerRemainingOp),
			)

			return TextEdit{
				Kind:    KindReplace,
				From:    from,
				To:      to,
				Content: content,
			}
		}

		return TextEdit{
			Kind:    KindDelete,
			From:    from,
			To:      to,
			Content: "",
		}
	}

	at := rng.Intn(n + 1)
	content := randomContent(
		rng,
		growthChunk(rng, avgBytesPerRemainingOp),
	)

	return TextEdit{
		Kind:    KindInsert,
		From:    at,
		To:      at,
		Content: content,
	}
}

// growthChunk varies the inserted size around the current average. It
// scales the average (4 if there is none) by a factor in [0.4, 1.6), whose
// mean is 1.0, and caps the result at 64 runes so single edits stay small.
func growthChunk(rng *rand.Rand, avgBytesPerRemainingOp int) int {
	base := avgBytesPerRemainingOp

	if base <= 0 {
		base = 4
	}

	factor := 0.4 + rng.Float64()*1.2
	size := min(max(int(float64(base)*factor), 1), 64)

	return size
}

// randomRange returns a random range with the given span
func randomRange(rng *rand.Rand, n int, span int) (from, to int) {
	if span > n {
		span = n
	}

	if span < 1 {
		span = 1
	}

	from = rng.Intn(n - span + 1)
	to = from + span

	return from, to
}

// deleteSpan returns the length of a delete range. Most deletes are short,
// with a few larger ranges mixed in: 85% remove 1-5 runes and the rest
// remove 5-34 runes.
func deleteSpan(rng *rand.Rand) int {
	if rng.Float64() < 0.85 {
		return 1 + rng.Intn(5)
	}

	return 5 + rng.Intn(30)
}

// randomContent mixes words, sentence-shaped text, and short random strings.
// Each appended piece is a 1-4 character random string with 8% probability,
// a sentence template with 20% probability, and a single word otherwise.
func randomContent(rng *rand.Rand, targetRunes int) string {
	b := make([]rune, 0, targetRunes+64)

	for len(b) < targetRunes {
		roll := rng.Float64()

		switch {
		case roll < 0.08:
			b = append(
				b,
				randomGibberish(rng, 1+rng.Intn(4))...,
			)
		case roll < 0.08+0.20:
			b = append(b, []rune(randomSentence(rng))...)
		default:
			w := wordCorpus[rng.Intn(len(wordCorpus))]
			b = append(b, []rune(w)...)
		}

		if len(b) < targetRunes {
			b = append(b, ' ')
		}
	}

	if len(b) > targetRunes {
		b = b[:targetRunes]
	}

	return string(b)
}

func randomGibberish(rng *rand.Rand, n int) []rune {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

	out := make([]rune, n)

	for i := range out {
		out[i] = rune(alphabet[rng.Intn(len(alphabet))])
	}

	return out
}

// Applies each edit with a separate Update call
// The change count is taken from the resulting change pack
func ApplyToDocument(
	doc *document.Document,
	textKey string,
	ops []TextEdit,
) (applied int, err error) {
	for i, op := range ops {
		updateErr := doc.Update(
			func(root *json.Object, _ *presence.Presence) error {
				text := root.GetText(textKey)
				if text == nil {
					text = root.SetNewText(textKey)
				}
				text.Edit(op.From, op.To, op.Content)
				return nil
			},
			fmt.Sprintf("workload op #%d: %s", i, op),
		)

		if updateErr != nil {
			return applied, fmt.Errorf(
				"workload: op %d (%s) failed: %w",
				i,
				op,
				updateErr,
			)
		}
	}

	pack := doc.CreateChangePack()
	applied = len(pack.Changes)

	return applied, nil
}

const testTextKey = "content"

// workloadCacheKey identifies a generated workload. The same
// (totalChanges, seed) pair always produces the same edits and Changes.
type workloadCacheKey struct {
	totalChanges int
	seed         int64
}

// cachedWorkload holds the generated Changes and the final text size.
// The Changes are shared between callers and must be treated as read-only.
type cachedWorkload struct {
	changes     []*change.Change
	actualBytes int
}

var workloadCache = struct {
	sync.Mutex
	entries map[workloadCacheKey]*cachedWorkload
}{entries: make(map[workloadCacheKey]*cachedWorkload)}

// includeLargeWorkload reports whether the expensive 3MB cases should run.
// They are opt-in: set YORKIE_BENCH_LARGE and do not use -short.
func includeLargeWorkload() bool {
	return os.Getenv("YORKIE_BENCH_LARGE") != "" && !testing.Short()
}

// getOrCreateWorkload generates the workload once per (totalChanges, seed)
// and reuses it afterwards, so a size/gap matrix does not regenerate the same
// tens of thousands of Update calls for every case.
func getOrCreateWorkload(
	tb testing.TB,
	docKey key.Key,
	totalChanges int,
	seed int64,
) (*cachedWorkload, error) {
	cacheKey := workloadCacheKey{totalChanges: totalChanges, seed: seed}

	workloadCache.Lock()
	defer workloadCache.Unlock()

	if workload, ok := workloadCache.entries[cacheKey]; ok {
		return workload, nil
	}

	// Keep the document size roughly proportional to the change count. The
	// 40 bytes per change and the 0.2 DeleteRatio are the same as in
	// DefaultConfig.
	cfg := Config{
		TargetBytes:   totalChanges * 40,
		TargetChanges: totalChanges,
		DeleteRatio:   0.2,
		Seed:          seed,
	}

	ops, err := GenerateTextWorkload(cfg)
	if err != nil {
		return nil, err
	}

	doc := document.New(docKey)

	applied, err := ApplyToDocument(doc, testTextKey, ops)
	if err != nil {
		return nil, fmt.Errorf("apply workload (applied=%d): %w", applied, err)
	}

	if applied != totalChanges {
		return nil, fmt.Errorf(
			"applied changes (%d) differ from requested totalChanges (%d)",
			applied,
			totalChanges,
		)
	}

	actualBytes := len(extractText(tb, doc, testTextKey))

	pack := doc.CreateChangePack()
	if len(pack.Changes) != totalChanges {
		return nil, fmt.Errorf(
			"change count from CreateChangePack mismatch: got=%d want=%d",
			len(pack.Changes),
			totalChanges,
		)
	}

	workload := &cachedWorkload{changes: pack.Changes, actualBytes: actualBytes}
	workloadCache.entries[cacheKey] = workload

	tb.Logf(
		"generated workload cache entry: totalChanges=%d seed=%d ops=%d actualBytes=%d",
		totalChanges, seed, len(ops), actualBytes,
	)

	return workload, nil
}

// Checks that the generated workload reaches the target size on a real document
func TestGenerateTextWorkload_ConvergesToTargetSize(t *testing.T) {
	type tcase struct {
		name        string
		targetBytes int
	}
	cases := []tcase{
		{"50KB", 50 * 1024},
		{"500KB", 500 * 1024},
	}
	// 3MB takes ~90s locally and times out CI; opt in via YORKIE_BENCH_LARGE.
	if includeLargeWorkload() {
		cases = append(cases, tcase{"3MB", 3 * 1024 * 1024})
	}

	for _, tc := range cases {

		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig(tc.targetBytes, 42)

			ops, err := GenerateTextWorkload(cfg)
			if err != nil {
				t.Fatalf(
					"GenerateTextWorkload failed: %v",
					err,
				)
			}

			doc := document.New(
				key.Key("workload-size-check-" + tc.name),
			)

			applied, err := ApplyToDocument(
				doc,
				testTextKey,
				ops,
			)

			if err != nil {
				t.Fatalf(
					"ApplyToDocument failed after %d applied changes: %v",
					applied,
					err,
				)
			}

			actualText := extractText(
				t,
				doc,
				testTextKey,
			)

			actualBytes := len(actualText)

			lower := int(float64(tc.targetBytes) * 0.9)
			upper := int(float64(tc.targetBytes) * 1.1)

			if actualBytes < lower || actualBytes > upper {
				t.Errorf(
					"actual size %d bytes is outside ±10%% of target %d bytes (want [%d, %d])",
					actualBytes,
					tc.targetBytes,
					lower,
					upper,
				)
			}

			preview := []rune(actualText)

			if len(preview) > 100 {
				preview = preview[:100]
			}

			t.Logf(
				"[%s] target=%d actualBytes=%d planOps=%d appliedChanges=%d\npreview: %q",
				tc.name,
				tc.targetBytes,
				actualBytes,
				len(ops),
				applied,
				string(preview),
			)
		})
	}
}

// Checks that generated text does not get dominated by a single word
func TestRandomContent_WordDiversity(t *testing.T) {
	rng := rand.New(rand.NewSource(7)) //nolint:gosec // seeded for reproducibility
	content := randomContent(rng, 20000)

	tokens := strings.Fields(content)
	if len(tokens) == 0 {
		t.Fatal("randomContent produced no tokens")
	}

	counts := make(map[string]int)
	for _, tok := range tokens {
		counts[strings.Trim(tok, ".,")]++
	}

	distinct := len(counts)
	maxFreq := 0
	var maxTok string
	for tok, c := range counts {
		if c > maxFreq {
			maxFreq = c
			maxTok = tok
		}
	}

	maxShare := float64(maxFreq) / float64(len(tokens))

	t.Logf(
		"tokens=%d distinct=%d mostFrequent=%q(%d, %.1f%% of tokens)",
		len(tokens), distinct, maxTok, maxFreq, maxShare*100,
	)

	if maxShare > 0.15 {
		t.Errorf(
			"token %q makes up %.1f%% of all tokens (want <= 15%%)",
			maxTok, maxShare*100,
		)
	}
}

// Extracts the text field from the marshaled document
func extractText(t testing.TB, doc *document.Document, key string) string {
	t.Helper()

	raw := doc.Marshal()

	var parsed map[string]stdjson.RawMessage
	if err := stdjson.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf(
			"failed to unmarshal doc.Marshal() output: %v\nraw: %s",
			err, raw,
		)
	}

	field, ok := parsed[key]
	if !ok {
		t.Fatalf("key %q not found in marshaled document: %s", key, raw)
	}

	var s string
	if err := stdjson.Unmarshal(field, &s); err == nil {
		return s
	}

	var nodes []struct {
		Val string `json:"val"`
	}
	if err := stdjson.Unmarshal(field, &nodes); err != nil {
		t.Fatalf(
			"key %q is neither a plain string nor a val-array in marshaled document (got: %v): %s",
			key, err, raw,
		)
	}

	var b strings.Builder
	for _, n := range nodes {
		b.WriteString(n.Val)
	}

	return b.String()
}

// seedDocumentAtGap seeds docKey with totalChanges generated edits pushed
// through the database, then moves the client checkpoint back by targetGap
// so that ServerSeq - Checkpoint.ServerSeq == targetGap. It returns the
// resulting DocInfo, the fixed checkpoint, and the byte size of the final
// text.
//
// targetGap is a client checkpoint distance, that is, how far the client lags
// behind the server. It is not the snapshot gap of the benchmark: the
// benchmark passes targetGap=0 and builds its own snapshot gap.
func seedDocumentAtGap(
	tb testing.TB,
	ctx context.Context,
	be *backend.Backend,
	docKey key.Key,
	clientInfo *database.ClientInfo,
	totalChanges int,
	targetGap int64,
	seed int64,
) (*database.DocInfo, change.Checkpoint, int) {
	tb.Helper()

	if targetGap < 0 || int64(totalChanges) < targetGap {
		tb.Fatalf("targetGap(%d) must be in [0, totalChanges(%d)]", targetGap, totalChanges)
	}

	// Get the DocInfo first so the generated changes can use its RefKey
	docInfoBefore, err := be.DB.FindOrCreateDocInfo(
		ctx,
		clientInfo.RefKey(),
		docKey,
		false, // disablePresence
	)
	if err != nil {
		tb.Fatalf("find or create DocInfo for key %s: %v", docKey, err)
	}

	// attach Client to this document.
	clientInfo, err = be.DB.TryAttaching(ctx, clientInfo.RefKey(), docInfoBefore.ID)
	if err != nil {
		tb.Fatalf("try attaching: %v", err)
	}

	if err := clientInfo.AttachDocument(
		docInfoBefore.ID, false, docInfoBefore.Epoch, 0, change.InitialCheckpoint,
	); err != nil {
		tb.Fatalf("attach document in memory: %v", err)
	}

	// The workload only depends on (totalChanges, seed), so it is generated
	// once and reused. The cached Changes are read-only; NewFromChange binds
	// each conversion to this document's RefKey, so it is not cached.
	workload, err := getOrCreateWorkload(tb, docKey, totalChanges, seed)
	if err != nil {
		tb.Fatalf("generate workload: %v", err)
	}

	actualBytes := workload.actualBytes

	pushables := make([]*database.ChangeInfo, 0, len(workload.changes))

	for _, cn := range workload.changes {
		info, err := database.NewFromChange(docInfoBefore.RefKey(), cn)
		if err != nil {
			tb.Fatalf("convert change to change info: %v", err)
		}

		pushables = append(pushables, info)
	}

	if len(pushables) != totalChanges {
		tb.Fatalf(
			"change count from CreateChangePack mismatch: got=%d want=%d",
			len(pushables),
			totalChanges,
		)
	}

	// Push all changes to advance ServerSeq.
	cpBeforePush := clientInfo.Checkpoint(docInfoBefore.ID)

	docInfo, cpAfterPush, err := be.DB.CreateChangeInfos(
		ctx,
		docInfoBefore.RefKey(),
		cpBeforePush,
		pushables,
		false,
	)
	if err != nil {
		tb.Fatalf("seed changes: %v", err)
	}

	tb.Logf(
		"created %d changes, docInfo.ServerSeq=%d (before=%d), cpAfterPush=%s",
		len(pushables),
		docInfo.ServerSeq,
		docInfoBefore.ServerSeq,
		cpAfterPush,
	)

	if docInfo.ServerSeq < targetGap {
		tb.Fatalf(
			"targetGap exceeds serverSeq: serverSeq=%d targetGap=%d",
			docInfo.ServerSeq,
			targetGap,
		)
	}

	// Move the client checkpoint back to create the target gap
	fixedServerSeq := docInfo.ServerSeq - targetGap
	currentClientSeq := clientInfo.Checkpoint(docInfo.ID).ClientSeq
	fixedCheckpoint := change.NewCheckpoint(fixedServerSeq, currentClientSeq)

	if err := clientInfo.UpdateCheckpoint(docInfo.ID, fixedCheckpoint); err != nil {
		tb.Fatalf("fix checkpoint in memory: %v", err)
	}

	// Persist the checkpoint in the same path used by PushPull
	if err := be.DB.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo); err != nil {
		tb.Fatalf("persist checkpoint: %v", err)
	}

	// Read it back to check the persisted value
	reloaded, err := be.DB.FindClientInfoByRefKey(ctx, clientInfo.RefKey())
	if err != nil {
		tb.Fatalf("reload client info: %v", err)
	}

	reloadedCp := reloaded.Checkpoint(docInfo.ID)

	if reloadedCp.ServerSeq != fixedCheckpoint.ServerSeq {
		tb.Fatalf(
			"persisted checkpoint mismatch: got=%d want=%d",
			reloadedCp.ServerSeq,
			fixedCheckpoint.ServerSeq,
		)
	}

	// Check the final gap using the persisted checkpoint
	gap := docInfo.ServerSeq - reloadedCp.ServerSeq

	tb.Logf(
		"targetGap=%d serverSeq=%d clientCheckpointServerSeq=%d actualGap=%d",
		targetGap,
		docInfo.ServerSeq,
		reloadedCp.ServerSeq,
		gap,
	)

	if gap != targetGap {
		tb.Fatalf(
			"gap mismatch: got=%d want=%d",
			gap,
			targetGap,
		)
	}

	return docInfo, fixedCheckpoint, actualBytes
}

func TestSeedDocumentAtGap(t *testing.T) {
	gaps := []int64{50, 500, 1000}

	for _, targetGap := range gaps {

		t.Run(fmt.Sprintf("gap-%d", targetGap), func(t *testing.T) {
			ctx := context.Background()

			projectInfo, err := testBackend.DB.FindProjectInfoByID(
				ctx,
				database.DefaultProjectID,
			)
			assert.NoError(t, err)

			project := projectInfo.ToProject()

			activateResp, err := testClient.ActivateClient(
				ctx,
				connect.NewRequest(&api.ActivateClientRequest{
					ClientKey: fmt.Sprintf("seed-gap-test-%d", targetGap),
				}),
			)
			assert.NoError(t, err)

			clientInfo, err := testBackend.DB.FindClientInfoByRefKey(
				ctx,
				types.ClientRefKey{
					ProjectID: project.ID,
					ClientID:  types.ID(activateResp.Msg.ClientId),
				},
			)
			assert.NoError(t, err)

			docKey := key.Key(
				fmt.Sprintf("seed-gap-test-%d", targetGap),
			)

			docInfo, fixedCheckpoint, _ := seedDocumentAtGap(
				t,
				ctx,
				testBackend,
				docKey,
				clientInfo,
				int(targetGap),
				targetGap,
				42, // fixed seed, same workload as the benchmark
			)

			actualGap := docInfo.ServerSeq - fixedCheckpoint.ServerSeq

			if actualGap != targetGap {
				t.Fatalf(
					"gap mismatch: got=%d want=%d",
					actualGap,
					targetGap,
				)
			}
		})
	}
}

// activateTestClient activates a client and returns its ClientInfo
// from the database
func activateTestClient(tb testing.TB, ctx context.Context, clientKey string) *database.ClientInfo {
	tb.Helper()

	projectInfo, err := testBackend.DB.FindProjectInfoByID(ctx, database.DefaultProjectID)
	assert.NoError(tb, err)
	project := projectInfo.ToProject()

	activateResp, err := testClient.ActivateClient(
		ctx,
		connect.NewRequest(&api.ActivateClientRequest{ClientKey: clientKey}),
	)
	assert.NoError(tb, err)

	clientInfo, err := testBackend.DB.FindClientInfoByRefKey(
		ctx,
		types.ClientRefKey{
			ProjectID: project.ID,
			ClientID:  types.ID(activateResp.Msg.ClientId),
		},
	)
	assert.NoError(tb, err)
	return clientInfo
}

// TestWorkloadMatrix_SizeAndGap checks that each document size and gap
// combination produces the expected state. By default it covers 6 subtests: 2 document
// sizes (50KB, 500KB) x 3 client gaps (50, 500, 1000); 3MB is added when
// YORKIE_BENCH_LARGE is set. It validates the
// workload and seeding matrix independently of the snapshot benchmark, so it
// has no Cache axis and does not build a snapshot.
func TestWorkloadMatrix_SizeAndGap(t *testing.T) {
	type sizeCase struct {
		name  string
		bytes int
	}
	sizeCases := []sizeCase{
		{"50KB", 50 * 1024},
		{"500KB", 500 * 1024},
	}
	// 3MB regenerates tens of thousands of Updates and times out CI; opt in.
	if includeLargeWorkload() {
		sizeCases = append(sizeCases, sizeCase{"3MB", 3 * 1024 * 1024})
	}

	gapCases := []int64{50, 500, 1000}

	for _, sz := range sizeCases {

		for _, gap := range gapCases {

			t.Run(fmt.Sprintf("%s/gap-%d", sz.name, gap), func(t *testing.T) {
				ctx := context.Background()

				clientKey := fmt.Sprintf("matrix-%s-gap-%d", sz.name, gap)
				clientInfo := activateTestClient(t, ctx, clientKey)

				docKey := key.Key(
					fmt.Sprintf("matrix-doc-%s-gap-%d", sz.name, gap),
				)

				// Keep the same change-to-size ratio as DefaultConfig
				// and make sure there are enough changes to create the target gap
				totalChanges := max(sz.bytes/40, 1)
				if int64(totalChanges) < gap {
					totalChanges = int(gap)
				}

				docInfo, fixedCheckpoint, actualBytes := seedDocumentAtGap(
					t,
					ctx,
					testBackend,
					docKey,
					clientInfo,
					totalChanges,
					gap,
					42, // fixed seed, same workload as the benchmark
				)

				// Check that the generated document is within ±10% of the target size
				lower := int(float64(sz.bytes) * 0.9)
				upper := int(float64(sz.bytes) * 1.1)

				if actualBytes < lower || actualBytes > upper {
					t.Errorf(
						"actual size %d bytes is outside ±10%% of target %d bytes (want [%d, %d])",
						actualBytes,
						sz.bytes,
						lower,
						upper,
					)
				}

				// Check that the persisted checkpoint produces the target gap
				actualGap := docInfo.ServerSeq - fixedCheckpoint.ServerSeq
				if actualGap != gap {
					t.Fatalf(
						"gap mismatch: got=%d want=%d",
						actualGap,
						gap,
					)
				}

				t.Logf(
					"[%s/gap-%d] totalChanges=%d actualBytes=%d (target=%d) actualGap=%d",
					sz.name,
					gap,
					totalChanges,
					actualBytes,
					sz.bytes,
					actualGap,
				)
			})
		}
	}
}
