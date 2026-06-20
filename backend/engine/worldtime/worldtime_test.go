package worldtime

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/core"
)

// ---------------------------------------------------------------------------
// AC1: DefaultConfig satisfies Validate and yields expected defaults.
// ---------------------------------------------------------------------------

func TestDefaultConfig_Validate(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("DefaultConfig().Validate() = %v, want nil", err)
	}
	if cfg.TickMinutes != 1 {
		t.Errorf("DefaultConfig().TickMinutes = %d, want 1", cfg.TickMinutes)
	}
	if cfg.DayMinutes != 1440 {
		t.Errorf("DefaultConfig().DayMinutes = %d, want 1440", cfg.DayMinutes)
	}
	if cfg.DaysPerSeason != 30 {
		t.Errorf("DefaultConfig().DaysPerSeason = %d, want 30", cfg.DaysPerSeason)
	}
	if cfg.SeasonsPerYear != 4 {
		t.Errorf("DefaultConfig().SeasonsPerYear = %d, want 4", cfg.SeasonsPerYear)
	}
}

// ---------------------------------------------------------------------------
// AC2: Validate rejects every field <= 0 (table-driven).
// ---------------------------------------------------------------------------

func TestValidate_RejectsNonPositiveFields(t *testing.T) {
	good := DefaultConfig()

	cases := []struct {
		name string
		cfg  Config
	}{
		{"TickMinutes=0", func() Config { c := good; c.TickMinutes = 0; return c }()},
		{"TickMinutes=-1", func() Config { c := good; c.TickMinutes = -1; return c }()},
		{"DayMinutes=0", func() Config { c := good; c.DayMinutes = 0; return c }()},
		{"DayMinutes=-5", func() Config { c := good; c.DayMinutes = -5; return c }()},
		{"DaysPerSeason=0", func() Config { c := good; c.DaysPerSeason = 0; return c }()},
		{"DaysPerSeason=-1", func() Config { c := good; c.DaysPerSeason = -1; return c }()},
		{"SeasonsPerYear=0", func() Config { c := good; c.SeasonsPerYear = 0; return c }()},
		{"SeasonsPerYear=-3", func() Config { c := good; c.SeasonsPerYear = -3; return c }()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.cfg.Validate(); err == nil {
				t.Error("Validate() = nil, want error")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Helper: default clock for tests (must not error).
// ---------------------------------------------------------------------------

func defaultClock(t *testing.T) Clock {
	t.Helper()
	clk, err := NewClock(DefaultConfig())
	if err != nil {
		t.Fatalf("NewClock(DefaultConfig()) = %v, want nil", err)
	}
	return clk
}

// ---------------------------------------------------------------------------
// AC3: Table test against hand-computed values.
// ---------------------------------------------------------------------------

func TestClock_Accessors_HandComputed(t *testing.T) {
	clk := defaultClock(t)

	// With TickMinutes=1, DayMinutes=1440, DaysPerSeason=30, SeasonsPerYear=4:
	// - t=0       -> gm=0
	// - t=1439    -> gm=1439, Day 0, minute 1439, hour 23
	// - t=1440    -> gm=1440, Day 1, minute 0,   hour 0
	// - t=43199   -> gm=43199 = 29*1440+1439 -> Day 29, minute 1439, hour 23, Season 0
	// - t=43200   -> gm=43200 = 30*1440      -> Day 30, minute 0,   hour 0,  Season 1
	// - t=172799  -> last tick of year 0 (day 119, min 1439, hour 23, season 3)
	// - t=172800  -> first tick of year 1 (day 120, min 0, hour 0, season 0)

	cases := []struct {
		name           string
		t              core.Tick
		wantMin        core.GameMinutes
		wantMinOfDay   int64
		wantHour       int
		wantDay        int64
		wantSeason     int
		wantYear       int64
	}{
		{"t=0 (epoch)", 0, 0, 0, 0, 0, 0, 0},
		{"t=1439 (end day 0)", 1439, 1439, 1439, 23, 0, 0, 0},
		{"t=1440 (start day 1)", 1440, 1440, 0, 0, 1, 0, 0},
		{"t=2880 (start day 2)", 2880, 2880, 0, 0, 2, 0, 0},
		{"t=43199 (end day 29)", 43199, 43199, 1439, 23, 29, 0, 0},
		{"t=43200 (start season 1)", 43200, 43200, 0, 0, 30, 1, 0},
		{"t=172799 (end year 0)", 172799, 172799, 1439, 23, 119, 3, 0},
		{"t=172800 (start year 1)", 172800, 172800, 0, 0, 120, 0, 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clk.Minutes(tc.t); got != tc.wantMin {
				t.Errorf("Minutes(%d) = %d, want %d", tc.t, got, tc.wantMin)
			}
			if got := clk.MinuteOfDay(tc.t); got != tc.wantMinOfDay {
				t.Errorf("MinuteOfDay(%d) = %d, want %d", tc.t, got, tc.wantMinOfDay)
			}
			if got := clk.HourOfDay(tc.t); got != tc.wantHour {
				t.Errorf("HourOfDay(%d) = %d, want %d", tc.t, got, tc.wantHour)
			}
			if got := clk.DayOfRun(tc.t); got != tc.wantDay {
				t.Errorf("DayOfRun(%d) = %d, want %d", tc.t, got, tc.wantDay)
			}
			if got := clk.Season(tc.t); got != tc.wantSeason {
				t.Errorf("Season(%d) = %d, want %d", tc.t, got, tc.wantSeason)
			}
			if got := clk.Year(tc.t); got != tc.wantYear {
				t.Errorf("Year(%d) = %d, want %d", tc.t, got, tc.wantYear)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// AC4: Bounds property test -- HourOfDay in [0,24) and MinuteOfDay in [0,DayMinutes)
// for a sweep of tick (0 ... 3xyear).
// ---------------------------------------------------------------------------

func TestClock_Bounds_Property(t *testing.T) {
	clk := defaultClock(t)
	cfg := DefaultConfig()
	ticksPer3Years := 3 * cfg.DaysPerSeason * cfg.SeasonsPerYear * cfg.DayMinutes / cfg.TickMinutes

	for tick := core.Tick(0); tick < core.Tick(ticksPer3Years); tick++ {
		mod := clk.MinuteOfDay(tick)
		if mod < 0 || mod >= cfg.DayMinutes {
			t.Errorf("tick=%d: MinuteOfDay=%d out of [0, %d)", tick, mod, cfg.DayMinutes)
			break
		}
		h := clk.HourOfDay(tick)
		if h < 0 || h >= 24 {
			t.Errorf("tick=%d: HourOfDay=%d out of [0,24)", tick, h)
			break
		}
		season := clk.Season(tick)
		if season < 0 || season >= int(cfg.SeasonsPerYear) {
			t.Errorf("tick=%d: Season=%d out of [0, %d)", tick, season, cfg.SeasonsPerYear)
			break
		}
	}
}

// ---------------------------------------------------------------------------
// AC5: At(t) returns fields equal to the individual accessors.
// ---------------------------------------------------------------------------

func TestClock_At_CrossCheck(t *testing.T) {
	clk := defaultClock(t)

	ticks := []core.Tick{0, 1, 600, 1439, 1440, 10000, 43199, 43200, 100000, 172799, 172800, 500000}
	for _, tick := range ticks {
		cal := clk.At(tick)
		if cal.Minute != clk.Minutes(tick) {
			t.Errorf("tick=%d: At.Minute=%d != Minutes=%d", tick, cal.Minute, clk.Minutes(tick))
		}
		if cal.HourOfDay != clk.HourOfDay(tick) {
			t.Errorf("tick=%d: At.HourOfDay=%d != HourOfDay=%d", tick, cal.HourOfDay, clk.HourOfDay(tick))
		}
		if cal.DayOfRun != clk.DayOfRun(tick) {
			t.Errorf("tick=%d: At.DayOfRun=%d != DayOfRun=%d", tick, cal.DayOfRun, clk.DayOfRun(tick))
		}
		if cal.Season != clk.Season(tick) {
			t.Errorf("tick=%d: At.Season=%d != Season=%d", tick, cal.Season, clk.Season(tick))
		}
		if cal.Year != clk.Year(tick) {
			t.Errorf("tick=%d: At.Year=%d != Year=%d", tick, cal.Year, clk.Year(tick))
		}
	}
}

// ---------------------------------------------------------------------------
// AC6: Monotonicity -- Minutes is non-decreasing over an increasing tick sweep.
// ---------------------------------------------------------------------------

func TestClock_Monotonicity(t *testing.T) {
	clk := defaultClock(t)
	var prev core.GameMinutes
	for tick := core.Tick(0); tick < 100000; tick++ {
		cur := clk.Minutes(tick)
		if cur < prev {
			t.Fatalf("non-monotonic at tick=%d: %d < %d", tick, cur, prev)
		}
		prev = cur
	}
}

// ---------------------------------------------------------------------------
// AC7: TicksForMinutes(Minutes(t)) == t when TickMinutes divides evenly.
// ---------------------------------------------------------------------------

func TestClock_TicksForMinutes_RoundTrip(t *testing.T) {
	clk := defaultClock(t)

	ticks := []core.Tick{0, 1, 100, 1440, 100000, 172799, 172800}
	for _, tick := range ticks {
		gm := clk.Minutes(tick)
		back := clk.TicksForMinutes(gm)
		if back != tick {
			t.Errorf("TicksForMinutes(Minutes(%d)) = %d, want %d", tick, back, tick)
		}
	}
}

// ---------------------------------------------------------------------------
// AC7b: TicksForMinutes floor behaviour.
// ---------------------------------------------------------------------------

func TestClock_TicksForMinutes_Floor(t *testing.T) {
	cfg := Config{TickMinutes: 2, DayMinutes: 1440, DaysPerSeason: 30, SeasonsPerYear: 4}
	clk, err := NewClock(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if got := clk.TicksForMinutes(1); got != 0 {
		t.Errorf("TicksForMinutes(1) with TickMinutes=2 = %d, want 0", got)
	}
	if got := clk.TicksForMinutes(2); got != 1 {
		t.Errorf("TicksForMinutes(2) with TickMinutes=2 = %d, want 1", got)
	}
	if got := clk.TicksForMinutes(3); got != 1 {
		t.Errorf("TicksForMinutes(3) with TickMinutes=2 = %d, want 1", got)
	}
	if got := clk.TicksForMinutes(4); got != 2 {
		t.Errorf("TicksForMinutes(4) with TickMinutes=2 = %d, want 2", got)
	}
}

// ---------------------------------------------------------------------------
// AC8: Static check -- no "time" package import in worldtime.go.
// ---------------------------------------------------------------------------

func TestNoTimePackageImport(t *testing.T) {
	// Use `go list -f` to extract only the direct Imports field (not transitive Deps).
	cmd := exec.Command("go", "list", "-f", "{{.Imports}}", "github.com/dogring/bdg/engine/worldtime")
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("go list failed: %v -- skipping static check", err)
	}
	if strings.Contains(string(out), `"time"`) {
		t.Error("package directly imports the 'time' package -- violates D12 (no wall-clock)")
	}
}

// ---------------------------------------------------------------------------
// AC9: Determinism -- 10 000 repeated At(tick) calls for fixed tick are
// byte-identical.
// ---------------------------------------------------------------------------

func TestClock_Determinism(t *testing.T) {
	clk := defaultClock(t)
	tick := core.Tick(1234567)

	first := clk.At(tick)
	for i := 0; i < 10000; i++ {
		got := clk.At(tick)
		if got != first {
			t.Fatalf("iteration %d: At(%d) = %+v, want %+v", i, tick, got, first)
		}
	}
}

// ---------------------------------------------------------------------------
// NewClock returns error for invalid config.
// ---------------------------------------------------------------------------

func TestNewClock_InvalidConfig(t *testing.T) {
	_, err := NewClock(Config{})
	if err == nil {
		t.Error("NewClock(Config{}) = nil, want error")
	}
}

// ---------------------------------------------------------------------------
// Additional: non-default TickMinutes (e.g. 5 game-minutes per tick).
// ---------------------------------------------------------------------------

func TestClock_NonDefaultTickMinutes(t *testing.T) {
	cfg := Config{TickMinutes: 5, DayMinutes: 1440, DaysPerSeason: 30, SeasonsPerYear: 4}
	clk, err := NewClock(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if got := clk.Minutes(0); got != 0 {
		t.Errorf("Minutes(0) = %d, want 0", got)
	}
	if got := clk.Minutes(1); got != 5 {
		t.Errorf("Minutes(1) = %d, want 5", got)
	}
	if got := clk.DayOfRun(288); got != 1 {
		t.Errorf("DayOfRun(288) = %d, want 1", got)
	}
	if got := clk.MinuteOfDay(288); got != 0 {
		t.Errorf("MinuteOfDay(288) = %d, want 0", got)
	}
}
