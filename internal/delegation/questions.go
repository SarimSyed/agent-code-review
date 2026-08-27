// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package delegation

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var reviewCallPattern = regexp.MustCompile(`^(?:.*?\b)?(await\s+)?([A-Za-z_$][A-Za-z0-9_$]*(?:\.[A-Za-z_$][A-Za-z0-9_$]*)+)\s*\((.*)$`)

type reviewChangedLine struct {
	text    string
	line    int
	call    string
	args    string
	awaited bool
}

func generateReviewQuestions(units []ReviewUnit) []ReviewQuestion {
	questions := make([]ReviewQuestion, 0)
	seen := map[string]bool{}
	for _, unit := range units {
		for _, file := range unit.Files {
			removed, added := changedReviewLines(file.Diff)
			addedByCall := map[string][]reviewChangedLine{}
			for _, line := range added {
				if line.call != "" {
					addedByCall[line.call] = append(addedByCall[line.call], line)
				}
			}
			for _, old := range removed {
				if old.call == "" {
					continue
				}
				if replacements := addedByCall[old.call]; len(replacements) > 0 {
					current := replacements[0]
					if old.awaited && !current.awaited {
						questions = appendReviewQuestion(questions, seen, ReviewQuestion{
							Kind: "dependency_order", UnitID: unit.ID, File: file.Path, Line: current.line, Subject: old.call,
							Question: "Was a required happens-before dependency lost when this call moved from sequential await into concurrent or unawaited execution? Trace every consumer initialized in the same region.",
							Evidence: fmt.Sprintf("removed `%s`; added `%s`", strings.TrimSpace(old.text), strings.TrimSpace(current.text)),
						})
					}
					if hasCallArguments(old.args) && !hasCallArguments(current.args) {
						questions = appendReviewQuestion(questions, seen, ReviewQuestion{
							Kind: "api_contract", UnitID: unit.ID, File: file.Path, Line: current.line, Subject: old.call,
							Question: "Are the removed arguments optional at every reachable call path? Inspect the callee signature, guards, uses, errors, and focused tests.",
							Evidence: fmt.Sprintf("removed `%s`; added `%s`", strings.TrimSpace(old.text), strings.TrimSpace(current.text)),
						})
					}
					continue
				}
				if lifecycleCall(old.call) && receiverStillPresent(file.Content, old.call) {
					questions = appendReviewQuestion(questions, seen, ReviewQuestion{
						Kind: "lifecycle", UnitID: unit.ID, File: file.Path, Line: old.line, Subject: old.call,
						Question: "What behavior, listener, registration, cleanup, or state transition did this removed call provide, and where is its replacement? Search production callers and tests.",
						Evidence: fmt.Sprintf("removed `%s` while its receiver remains in the target file", strings.TrimSpace(old.text)),
					})
				}
			}
		}
	}
	for i := range questions {
		questions[i].ID = fmt.Sprintf("question-%04d", i+1)
	}
	return questions
}

func appendReviewQuestion(questions []ReviewQuestion, seen map[string]bool, question ReviewQuestion) []ReviewQuestion {
	key := question.Kind + "\x00" + question.UnitID + "\x00" + question.File + "\x00" + question.Subject
	if seen[key] {
		return questions
	}
	seen[key] = true
	return append(questions, question)
}

func changedReviewLines(raw string) (removed, added []reviewChangedLine) {
	lineNo := 0
	inHunk := false
	for _, rawLine := range strings.Split(raw, "\n") {
		if match := hunkHeader.FindStringSubmatch(rawLine); match != nil {
			lineNo, _ = strconv.Atoi(match[1])
			inHunk = true
			continue
		}
		if !inHunk || strings.HasPrefix(rawLine, "\\ No newline") {
			continue
		}
		switch {
		case strings.HasPrefix(rawLine, "+"):
			added = append(added, parseReviewChangedLine(rawLine[1:], lineNo))
			lineNo++
		case strings.HasPrefix(rawLine, "-"):
			removed = append(removed, parseReviewChangedLine(rawLine[1:], lineNo))
		default:
			lineNo++
		}
	}
	return removed, added
}

func parseReviewChangedLine(text string, line int) reviewChangedLine {
	result := reviewChangedLine{text: text, line: line}
	match := reviewCallPattern.FindStringSubmatch(strings.TrimSpace(text))
	if match == nil {
		return result
	}
	result.awaited = strings.TrimSpace(match[1]) == "await"
	result.call = match[2]
	result.args = strings.TrimSpace(match[3])
	return result
}

func hasCallArguments(args string) bool {
	args = strings.TrimSpace(args)
	return args != "" && !strings.HasPrefix(args, ")")
}

func lifecycleCall(call string) bool {
	for _, suffix := range []string{".listen", ".register", ".subscribe", ".attach", ".mount", ".start", ".init"} {
		if strings.HasSuffix(call, suffix) {
			return true
		}
	}
	return false
}

func receiverStillPresent(content, call string) bool {
	receiver := strings.SplitN(call, ".", 2)[0]
	return receiver != "" && strings.Contains(content, receiver)
}
