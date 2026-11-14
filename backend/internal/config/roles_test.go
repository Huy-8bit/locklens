package config

import "testing"

func TestParseRoles(t *testing.T) {
	ok := map[string]Roles{
		"":                   AllRoles,
		"all":                AllRoles,
		" ALL ":              AllRoles,
		"api":                {API: true},
		"collector":          {Collector: true},
		"collector, jobs":    {Collector: true, Jobs: true},
		"api,collector,jobs": AllRoles,
		"jobs,api":           {API: true, Jobs: true},
	}
	for in, want := range ok {
		got, err := ParseRoles(in)
		if err != nil || got != want {
			t.Errorf("%q: got %+v %v, want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"web", "api,", ",", "all,api", "api,worker"} {
		if _, err := ParseRoles(in); err == nil {
			t.Errorf("%q: accepted", in)
		}
	}
	if !AllRoles.All() || (Roles{API: true}).All() {
		t.Error("All()")
	}
	if got := (Roles{Collector: true, Jobs: true}).List(); len(got) != 2 || got[0] != "collector" {
		t.Errorf("List: %v", got)
	}
}
