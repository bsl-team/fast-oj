package judge

// Status is the lifecycle of a submission inside the judging pipeline. It is
// stored as text, so the values here must match the schema CHECK constraint.
type Status string

const (
	Queued    Status = "queued"
	Compiling Status = "compiling"
	Running   Status = "running"
	Done      Status = "done"
	// Abandoned is set when a judge dies mid-run or its lease expires.
	Abandoned Status = "abandoned"
)

// InFlight statuses are the ones a client may still wait on.
func (s Status) InFlight() bool {
	return s == Queued || s == Compiling || s == Running
}

// Verdict is the final answer for a submission or a single case.
type Verdict string

const (
	AC  Verdict = "AC"
	WA  Verdict = "WA"
	TLE Verdict = "TLE"
	MLE Verdict = "MLE"
	RE  Verdict = "RE"
	CE  Verdict = "CE"
	// IE is an infrastructure fault (judge broke), never the submitter's fault.
	IE Verdict = "IE"
)

func (v Verdict) Accepted() bool { return v == AC }

// Transition reports whether moving from -> to is legal. A judge may only
// advance its own submission, and nothing may change a settled verdict, which
// keeps a stale judge from overwriting a newer result.
func Transition(from, to Status) bool {
	switch from {
	case Queued:
		return to == Compiling || to == Abandoned
	case Compiling, Running:
		return to == Running || to == Done || to == Abandoned
	case Done, Abandoned:
		// An abandoned row can be requeued by an operator.
		return from == Abandoned && to == Queued
	}
	return false
}

// VerdictName maps a raw string onto a known verdict, or IE when the judge sent
// something unrecognised.
func VerdictName(raw string) Verdict {
	for _, v := range []Verdict{AC, WA, TLE, MLE, RE, CE, IE} {
		if string(v) == raw {
			return v
		}
	}
	return IE
}
