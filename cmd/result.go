package main

type Result struct {
	moji      rune
	codepoint string
	id        string
	attr      string
	value     string
}

func (r *Result) Csv(isOutputValue bool) []string {
	if isOutputValue {
		return []string{r.codepoint, string(r.moji), r.id, r.attr, r.value}
	} else {
		return []string{r.codepoint, string(r.moji), r.id, r.attr}
	}
}

func ResultHeaderAry() []string {
	return []string{"コード", "文字", "識別番号", "属性", "値"}
}
