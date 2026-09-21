package eval

import "testing"

// 宪法测试四件套 + budget（make constitution 入口；规则可判定、模型无关）
func TestConstitutionSuites(t *testing.T) {
	for _, suite := range []string{"trust", "roundtrip", "parity", "conflict", "budget"} {
		reps, err := Run(suite)
		if err != nil {
			t.Fatalf("%s: %v", suite, err)
		}
		rep := reps[0]
		if !rep.Passed {
			for _, c := range rep.Checks {
				if !c.Passed {
					t.Errorf("[%s] %s: %s", suite, c.Name, c.Detail)
				}
			}
		}
	}
}
