package main

import (
	"encoding/json"
	"errors"
	"strings"
)

// Reuse semantic rules verbatim; change only the wire representation. This is
// an opt-in experiment, not provider-enforced schema generation.
var splitPrimaryPrompt = func() string {
	rules, _, _ := strings.Cut(typedPrimaryPrompt, "只对 system_fact 填 assessment：")
	rules = strings.ReplaceAll(rules, "输出一个 segments 数组，按语义拆分全文，每段指定 scope。只能使用以下六种类型：", "按语义拆分全文，业务事实放 facts，其他内容放 non_facts。语义分类规则如下：")
	rules = strings.ReplaceAll(rules, "只有此类型有 assessment，需要核对 reference。", "此类型放 facts，需要核对 reference。")
	return rules + `只输出两个数组 facts 和 non_facts，均必须存在，没有对应内容时填 []。
facts 每项固定含 quote、reason、status、evidence、checks，不含 scope 或 assessment。
status 为 supported/unsupported/uncertain。supported 的 evidence 必须非空且逐字摘录 reference，checks 至少一项；unsupported 的 evidence 为空，checks 至少一项；uncertain 仅用于无法消歧，evidence 为空。
checks 每项固定含 detail、relation、evidence。relation 为 entailed/not_stated/contradicted；entailed/contradicted 必须非空引用 reference，not_stated 必须空证据。缺失或冲突使事实 unsupported，抽象步骤不蕴含具体实现。
non_facts 每项固定且仅含 quote、scope、reason。scope 为 assistant_limitation/knowledge_limit/inquiry/social/correction；不含 status、evidence、checks、assessment，即使 null 也不允许。
quote 逐字摘录 answer；两个数组中 quote 的并集覆盖全部文字和数字，包括标题、序号和来源标记。总片段数 1 到 64。不得将业务事实归入 non_facts 来绕过核验。
只返回 JSON，无 Markdown 或额外字段：{"facts":[{"quote":"事实原文","reason":"理由","status":"supported","evidence":"资料原文","checks":[{"detail":"细节","relation":"entailed","evidence":"资料原文"}]}],"non_facts":[{"quote":"不能代办原文","scope":"assistant_limitation","reason":"助手限制"}]}。`
}()

type splitFact struct {
	Quote    string        `json:"quote"`
	Reason   string        `json:"reason"`
	Status   string        `json:"status"`
	Evidence string        `json:"evidence"`
	Checks   []detailCheck `json:"checks"`
}
type splitNonFact struct {
	Quote  string `json:"quote"`
	Scope  string `json:"scope"`
	Reason string `json:"reason"`
}
type splitOutput struct {
	Facts    []splitFact    `json:"facts"`
	NonFacts []splitNonFact `json:"non_facts"`
}

func parseSplit(raw string, input judgeInput) (judgment, string, error) {
	var output splitOutput
	if len(raw) > 128<<10 || strict([]byte(raw), &output) != nil || output.Facts == nil || output.NonFacts == nil || len(output.Facts)+len(output.NonFacts) == 0 || len(output.Facts)+len(output.NonFacts) > 64 {
		return judgment{}, "error", errors.New("invalid_split_primary")
	}
	typed := typedOutput{Segments: []typedSegment{}}
	for _, f := range output.Facts {
		a, err := json.Marshal(factAssessment{Status: f.Status, Evidence: f.Evidence, Checks: f.Checks})
		if err != nil {
			return judgment{}, "error", err
		}
		typed.Segments = append(typed.Segments, typedSegment{Quote: f.Quote, Scope: "system_fact", Reason: f.Reason, Assessment: a})
	}
	for _, n := range output.NonFacts {
		if n.Scope == "system_fact" {
			return judgment{}, "error", errors.New("fact_in_non_facts")
		}
		typed.Segments = append(typed.Segments, typedSegment{Quote: n.Quote, Scope: n.Scope, Reason: n.Reason})
	}
	converted, err := json.Marshal(typed)
	if err != nil {
		return judgment{}, "error", err
	}
	return parseTyped(string(converted), input)
}

func liveSplitJudge() (judgeFunc, string, error) {
	primary, model, err := liveJudgeForPrompt(splitPrimaryPrompt)
	if err != nil {
		return nil, "", err
	}
	reviewer, other, err := liveJudgeForPrompt(specificityPrompt)
	if err != nil {
		return nil, "", err
	}
	if model != other {
		return nil, "", errors.New("inconsistent model configuration")
	}
	return withPrimaryParser(primary, reviewer, parseSplit, "split_primary_parse"), model, nil
}
