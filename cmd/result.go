package cmd

type Result struct {
	Moji      rune
	Codepoint string
	Id        string
	Attr      string
	Value     string
}

func (r *Result) Csv(isOutputValue bool) []string {
	if isOutputValue {
		return []string{r.Codepoint, string(r.Moji), r.Id, r.Attr, r.Value}
	} else {
		return []string{r.Codepoint, string(r.Moji), r.Id, r.Attr}
	}
}

func ResultHeaderAry() []string {
	return []string{"コード", "文字", "識別番号", "属性", "値"}
}
