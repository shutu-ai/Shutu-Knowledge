package extension

import (
	"strings"
	"sync"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/evidence"
	"github.com/shutu-ai/shutu-knowledge/internal/knowledge"
)

const (
	autoMinInterval         = 5 * time.Minute
	autoAbsoluteScoreFloor  = 0.12
	autoStrongScoreMultiple = 2
	autoLeadRatio           = 1.2
	autoGroupRatio          = 0.6
	autoHistoryTurns        = 2
	autoShortQueryRunes     = 40
	autoInjectedMemoryMax   = 50
	autoSessionMemoryMax    = 128
)

type autoSession struct {
	lastInjectedAt time.Time
	keywords       []string
	injected       map[string]bool
	history        []string
}

var (
	autoSessionsMu sync.Mutex
	autoSessions   = map[string]*autoSession{}
)

func autoState(sessionID string) *autoSession {
	autoSessionsMu.Lock()
	defer autoSessionsMu.Unlock()
	state := autoSessions[sessionID]
	if state == nil {
		if len(autoSessions) >= autoSessionMemoryMax {
			autoSessions = map[string]*autoSession{}
		}
		state = &autoSession{injected: map[string]bool{}}
		autoSessions[sessionID] = state
	}
	return state
}

func autoSnapshot(sessionID string) (history, keywords []string, injected map[string]bool, lastInjectedAt time.Time) {
	state := autoState(sessionID)
	autoSessionsMu.Lock()
	defer autoSessionsMu.Unlock()
	history = append([]string{}, state.history...)
	keywords = append([]string{}, state.keywords...)
	injected = make(map[string]bool, len(state.injected))
	for id, ok := range state.injected {
		if ok {
			injected[id] = true
		}
	}
	return history, keywords, injected, state.lastInjectedAt
}

// rememberUserInput keeps only the latest bounded turns. The Agent owns
// conversation history; this is a small planning cache owned by Knowledge.
func rememberUserInput(sessionID, input string) {
	input = strings.TrimSpace(input)
	if sessionID == "" || input == "" {
		return
	}
	state := autoState(sessionID)
	autoSessionsMu.Lock()
	defer autoSessionsMu.Unlock()
	for _, previous := range state.history {
		if previous == input {
			return
		}
	}
	state.history = append(state.history, input)
	if len(state.history) > autoHistoryTurns {
		state.history = state.history[len(state.history)-autoHistoryTurns:]
	}
}

// externalHistory allows a compatible Agent to provide recent user turns
// without extending the public protocol. The value is an ordered list joined
// with the unit separator; the final item is nearest in time.
func externalHistory(metadata map[string]string) ([]string, bool) {
	if metadata == nil {
		return nil, false
	}
	raw := strings.TrimSpace(metadata["knowledge.recentUserInputs"])
	if raw == "" {
		return nil, false
	}
	parts := strings.Split(raw, "\x1f")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	if len(out) > autoHistoryTurns {
		out = out[len(out)-autoHistoryTurns:]
	}
	return out, true
}

type autoQueryPlan struct {
	primary  string
	enhanced string
}

var followUpSignals = []string{
	"那", "这个", "那个", "它", "上述", "前面", "后面", "接着", "然后", "继续", "呢",
	"that", "this", "it", "those", "these", "above", "previous", "next", "continue", "then",
}

func hasFollowUpSignal(text string) bool {
	lower := strings.ToLower(text)
	for _, signal := range followUpSignals {
		if strings.Contains(lower, signal) {
			return true
		}
	}
	return false
}

// planAutoQuery keeps the current turn authoritative. History is only added
// as a secondary query variant for short or deictic turns.
func planAutoQuery(rawText string, history []string) autoQueryPlan {
	primary := cleanAutoQuery(rawText)
	keywords := topicKeywords(retrieveKeywords(primary))
	needsHistory := len([]rune(strings.TrimSpace(rawText))) <= autoShortQueryRunes &&
		(hasFollowUpSignal(rawText) || len(keywords) < 2)
	if !needsHistory || len(history) == 0 {
		return autoQueryPlan{primary: primary}
	}
	enhanced := primary
	for index := len(history) - 1; index >= 0; index-- {
		cleaned := cleanAutoQuery(history[index])
		if cleaned == "" || cleaned == primary {
			continue
		}
		enhanced = boundQueryChars(enhanced+" "+cleaned, autoQueryChars)
	}
	if enhanced == primary {
		return autoQueryPlan{primary: primary}
	}
	return autoQueryPlan{primary: primary, enhanced: enhanced}
}

// pruneInjectedEvidence removes already-delivered chunks while preserving a
// fresh anchor and any unseen neighboring evidence.
func pruneInjectedEvidence(hit knowledge.SearchHit, injected map[string]bool) (knowledge.SearchHit, bool) {
	if injected[hit.ChunkID] {
		return knowledge.SearchHit{}, false
	}
	window := hit.ContextWindow
	if window == nil {
		return hit, true
	}
	if injected[window.AnchorChunkID] {
		return knowledge.SearchHit{}, false
	}
	next := *window
	next.Before = filterExcerpts(window.Before, injected)
	next.After = filterExcerpts(window.After, injected)
	next.HasMoreBefore = next.HasMoreBefore || len(next.Before) != len(window.Before)
	next.HasMoreAfter = next.HasMoreAfter || len(next.After) != len(window.After)
	hit.ContextWindow = &next
	return hit, true
}

func evidenceChunkIDs(hit knowledge.SearchHit) []string {
	ids := []string{hit.ChunkID}
	if hit.ContextWindow == nil {
		return ids
	}
	for _, excerpt := range hit.ContextWindow.Before {
		ids = append(ids, excerpt.ChunkID)
	}
	ids = append(ids, hit.ContextWindow.AnchorChunkID)
	for _, excerpt := range hit.ContextWindow.After {
		ids = append(ids, excerpt.ChunkID)
	}
	return ids
}

func commitInjection(sessionID string, keywords []string, hits []knowledge.SearchHit, injectedAt time.Time) {
	if sessionID == "" {
		return
	}
	state := autoState(sessionID)
	autoSessionsMu.Lock()
	defer autoSessionsMu.Unlock()
	state.lastInjectedAt = injectedAt
	state.keywords = keywords
	for _, hit := range hits {
		for _, id := range evidenceChunkIDs(hit) {
			if id != "" {
				state.injected[id] = true
			}
		}
	}
	for id := range state.injected {
		if len(state.injected) <= autoInjectedMemoryMax {
			break
		}
		delete(state.injected, id)
	}
}

func filterExcerpts(values []evidence.Excerpt, injected map[string]bool) []evidence.Excerpt {
	out := make([]evidence.Excerpt, 0, len(values))
	for _, excerpt := range values {
		if !injected[excerpt.ChunkID] {
			out = append(out, excerpt)
		}
	}
	return out
}
