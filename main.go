package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type FailureType string

const (
	FailNone     FailureType = "None"
	FailNetwork  FailureType = "Network"
	FailTimeout  FailureType = "Timeout"
	FailDatabase FailureType = "Database"
	FailUnknown  FailureType = "Unknown"
)

type Transaction struct {
	ID      string
	Amount  int
	Failure FailureType
}

var transactions = []Transaction{
	{"T001", 12000, FailNone},
	{"T002", 25000, FailNetwork},
	{"T003", 8000, FailNone},
	{"T004", 45000, FailTimeout},
	{"T005", 13000, FailNone},
	{"T006", 70000, FailDatabase},
	{"T007", 9000, FailNone},
	{"T008", 31000, FailNetwork},
	{"T009", 15000, FailNone},
	{"T010", 50000, FailTimeout},
	{"T011", 6000, FailNone},
	{"T012", 80000, FailDatabase},
	{"T013", 11000, FailNone},
	{"T014", 22000, FailNone},
	{"T015", 40000, FailNetwork},
}

var failingAttempts = map[FailureType]int{
	FailNone:     0,
	FailNetwork:  1,
	FailTimeout:  2,
	FailDatabase: 3,
}

const (
	maxRetries      = 2
	checkpointEvery = 5

	baseDelay = 1 * time.Second
)

func backoff(n int) time.Duration {
	return baseDelay << (n - 1)
}

type NetworkError struct {
	TxID    string
	Attempt int
}

func (e *NetworkError) Error() string {
	return fmt.Sprintf("%s: network failure in Process (%s)", e.TxID, attemptLabel(e.Attempt))
}

type TimeoutError struct {
	TxID    string
	Attempt int
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("%s: timeout in Process (%s)", e.TxID, attemptLabel(e.Attempt))
}

type DatabaseError struct {
	TxID    string
	Attempt int
}

func (e *DatabaseError) Error() string {
	return fmt.Sprintf("%s: database failure in Record (%s)", e.TxID, attemptLabel(e.Attempt))
}

func classify(err error) (FailureType, string) {
	var ne *NetworkError
	var te *TimeoutError
	var de *DatabaseError
	switch {
	case errors.As(err, &ne):
		return FailNetwork, "NetworkError"
	case errors.As(err, &te):
		return FailTimeout, "TimeoutError"
	case errors.As(err, &de):
		return FailDatabase, "DatabaseError"
	default:
		return FailUnknown, fmt.Sprintf("%T", err)
	}
}

func retryable(f FailureType) bool {
	return f == FailNetwork || f == FailTimeout || f == FailDatabase
}

type PaymentService struct {
	attempts map[string]int
}

func NewPaymentService() *PaymentService {
	return &PaymentService{attempts: make(map[string]int)}
}

func (s *PaymentService) Execute(tx Transaction) error {
	attempt := s.attempts[tx.ID]
	s.attempts[tx.ID]++
	fails := attempt < failingAttempts[tx.Failure]

	if err := validate(tx); err != nil {
		return err
	}
	if err := process(tx, attempt, fails); err != nil {
		return err
	}
	return record(tx, attempt, fails)
}

func validate(tx Transaction) error {
	if tx.ID == "" || tx.Amount <= 0 {
		return fmt.Errorf("%s: invalid transaction (amount %d)", tx.ID, tx.Amount)
	}
	return nil
}

func process(tx Transaction, attempt int, fails bool) error {
	if !fails {
		return nil
	}
	switch tx.Failure {
	case FailNetwork:
		return &NetworkError{TxID: tx.ID, Attempt: attempt}
	case FailTimeout:
		return &TimeoutError{TxID: tx.ID, Attempt: attempt}
	}
	return nil
}

func record(tx Transaction, attempt int, fails bool) error {
	if fails && tx.Failure == FailDatabase {
		return &DatabaseError{TxID: tx.ID, Attempt: attempt}
	}
	return nil
}

type Metrics struct {
	Attempted       int
	Successful      int
	Failed          int
	ProcessedAmount int
	LostAmount      int
	Retries         int
	Rollbacks       int
}

func (m *Metrics) finalize() {
	total := 0
	for _, tx := range transactions {
		total += tx.Amount
	}
	m.Failed = len(transactions) - m.Successful
	m.LostAmount = total - m.ProcessedAmount
}

func (m Metrics) CompletionRate() float64 {
	return float64(m.Successful) / float64(len(transactions)) * 100
}

func runBaseline(log *slog.Logger) Metrics {
	svc := NewPaymentService()
	var m Metrics
	for _, tx := range transactions {
		m.Attempted++
		if err := svc.Execute(tx); err != nil {
			log.Error("processor stopped", "tx", tx.ID, "amount", tx.Amount, "error", err.Error())
			break
		}
		m.Successful++
		m.ProcessedAmount += tx.Amount
		log.Info("transaction processed", "tx", tx.ID, "amount", tx.Amount)
	}
	m.finalize()
	return m
}

type ExceptionRow struct {
	TxID, Failure, Exception, Action, Status string
}

func runExceptionHandling(log *slog.Logger) (Metrics, []ExceptionRow) {
	svc := NewPaymentService()
	var m Metrics
	var rows []ExceptionRow
	for _, tx := range transactions {
		m.Attempted++
		err := svc.Execute(tx)
		if err != nil {
			kind, name := classify(err)
			log.Warn("transaction failed, continuing",
				"tx", tx.ID, "amount", tx.Amount, "failure", kind, "exception", name, "error", err.Error())
			rows = append(rows, ExceptionRow{tx.ID, string(kind), name, "Logged, skipped, continued", "FAILED"})
			continue
		}
		m.Successful++
		m.ProcessedAmount += tx.Amount
		log.Info("transaction processed", "tx", tx.ID, "amount", tx.Amount)
	}
	m.finalize()
	return m, rows
}

type AttemptRow struct {
	Step    int
	Time    string
	TxID    string
	Attempt string
	Failure string
	Action  string
	Result  string
}

type Ledger struct {
	IDs   []string
	Total int
}

func (l *Ledger) Has(id string) bool {
	for _, x := range l.IDs {
		if x == id {
			return true
		}
	}
	return false
}

type Checkpoint struct {
	Name  string
	Count int
	Total int
	IDs   []string
}

func attemptLabel(a int) string {
	if a == 0 {
		return "Initial"
	}
	return fmt.Sprintf("Retry %d", a)
}

func rollback(ledger *Ledger, cps []Checkpoint) (target string, discarded int) {
	if len(cps) == 0 {
		discarded = len(ledger.IDs)
		*ledger = Ledger{}
		return "initial state", discarded
	}
	cp := cps[len(cps)-1]
	discarded = len(ledger.IDs) - cp.Count
	ledger.IDs = append([]string(nil), cp.IDs...)
	ledger.Total = cp.Total
	return cp.Name, discarded
}

func runFaultTolerant(log *slog.Logger) (Metrics, []AttemptRow, []Checkpoint) {
	svc := NewPaymentService()
	var m Metrics
	var ledger Ledger
	var cps []Checkpoint
	var rows []AttemptRow
	step := 0

	for _, tx := range transactions {
		m.Attempted++
		succeeded := false

		for attempt := 0; attempt <= maxRetries; attempt++ {
			if attempt > 0 {
				m.Retries++
			}
			step++
			row := AttemptRow{
				Step:    step,
				Time:    time.Now().Format("15:04:05.000"),
				TxID:    tx.ID,
				Attempt: attemptLabel(attempt),
				Failure: string(tx.Failure),
			}

			err := svc.Execute(tx)
			if err == nil {
				if ledger.Has(tx.ID) {
					row.Action, row.Result = "Duplicate ignored", "SKIPPED"
					rows = append(rows, row)
					log.Warn("duplicate transaction ignored", "step", step, "tx", tx.ID)
					break
				}
				ledger.IDs = append(ledger.IDs, tx.ID)
				ledger.Total += tx.Amount
				row.Action, row.Result = "Record", "SUCCESS"
				rows = append(rows, row)
				log.Info("attempt succeeded", "step", step, "tx", tx.ID, "attempt", row.Attempt, "amount", tx.Amount)
				succeeded = true
				break
			}

			kind, name := classify(err)
			row.Result = "FAIL"
			lastAttempt := attempt == maxRetries || !retryable(kind)

			switch {
			case !lastAttempt:
				row.Action = fmt.Sprintf("Retry after %s", backoff(attempt+1))
			case kind == FailDatabase:
				target, discarded := rollback(&ledger, cps)
				m.Rollbacks++
				row.Action = "Rollback to " + target
				row.Result = "ROLLBACK"
				log.Warn("rollback", "tx", tx.ID, "to", target,
					"discarded_after_checkpoint", discarded,
					"ledger_count", len(ledger.IDs), "ledger_total", ledger.Total)
			default:
				row.Action = "Give up"
			}
			rows = append(rows, row)
			log.Warn("attempt failed", "step", step, "tx", tx.ID, "attempt", row.Attempt,
				"failure", kind, "exception", name, "action", row.Action, "error", err.Error())

			if lastAttempt {
				break
			}
			time.Sleep(backoff(attempt + 1))
		}

		if succeeded && len(ledger.IDs)%checkpointEvery == 0 {
			cp := Checkpoint{
				Name:  fmt.Sprintf("CP%d", len(cps)+1),
				Count: len(ledger.IDs),
				Total: ledger.Total,
				IDs:   append([]string(nil), ledger.IDs...),
			}
			cps = append(cps, cp)
			log.Info("checkpoint saved", "checkpoint", cp.Name, "count", cp.Count, "total", cp.Total, "ids", cp.IDs)
		}
	}

	m.Successful = len(ledger.IDs)
	m.ProcessedAmount = ledger.Total
	m.finalize()
	return m, rows, cps
}

func newLogger(name string) (*slog.Logger, func()) {
	if err := os.MkdirAll("logs", 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "cannot create logs dir:", err)
		os.Exit(1)
	}
	f, err := os.Create(filepath.Join("logs", name))
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot create log file:", err)
		os.Exit(1)
	}
	return slog.New(slog.NewJSONHandler(f, nil)), func() { f.Close() }
}

func pct(v float64) string { return fmt.Sprintf("%.2f%%", v) }

func main() {
	baseLog, closeBase := newLogger("baseline.jsonl")
	base := runBaseline(baseLog)
	closeBase()

	excLog, closeExc := newLogger("exception_handling.jsonl")
	exc, excRows := runExceptionHandling(excLog)
	closeExc()

	ftLog, closeFT := newLogger("fault_tolerant.jsonl")
	ft, attempts, cps := runFaultTolerant(ftLog)
	closeFT()

	fmt.Println("## Part A — Baseline")
	fmt.Println()
	fmt.Println("| Metric | Result |")
	fmt.Println("|---|---|")
	fmt.Printf("| Transactions attempted | %d |\n", base.Attempted)
	fmt.Printf("| Successful transactions | %d |\n", base.Successful)
	fmt.Printf("| Transactions lost | %d |\n", base.Failed)
	fmt.Printf("| Processed amount (KZT) | %d |\n", base.ProcessedAmount)
	fmt.Printf("| Lost amount (KZT) | %d |\n", base.LostAmount)
	fmt.Println()

	fmt.Println("## Part B — Exception Handling")
	fmt.Println()
	fmt.Println("| Transaction | Failure | Exception | Recovery Action | Final Status |")
	fmt.Println("|---|---|---|---|---|")
	for _, r := range excRows {
		fmt.Printf("| %s | %s | %s | %s | %s |\n", r.TxID, r.Failure, r.Exception, r.Action, r.Status)
	}
	fmt.Println()

	fmt.Println("## Part C — Retry Attempt Log (transactions with failures)")
	fmt.Println()
	fmt.Println("| Step / Timestamp | Transaction | Attempt | Failure | Action | Result |")
	fmt.Println("|---|---|---|---|---|---|")
	for _, r := range attempts {
		if r.Failure == string(FailNone) {
			continue
		}
		fmt.Printf("| %d / %s | %s | %s | %s | %s | %s |\n", r.Step, r.Time, r.TxID, r.Attempt, r.Failure, r.Action, r.Result)
	}
	fmt.Println()

	fmt.Println("## Part D — Checkpoints")
	fmt.Println()
	fmt.Println("| Checkpoint | Successful transactions | Total amount (KZT) | State saved |")
	fmt.Println("|---|---|---|---|")
	for _, cp := range cps {
		fmt.Printf("| %s | %d | %d | %s |\n", cp.Name, cp.Count, cp.Total, strings.Join(cp.IDs, ", "))
	}
	for i := len(cps) + 1; i <= 3; i++ {
		fmt.Printf("| CP%d | — | — | Not created: only %d successful transactions, %d needed |\n",
			i, ft.Successful, i*checkpointEvery)
	}
	fmt.Println()

	fmt.Println("## Part E — Before/After")
	fmt.Println()
	fmt.Println("| Metric | Baseline | Fault-tolerant | Improvement |")
	fmt.Println("|---|---|---|---|")
	fmt.Printf("| Successful transactions | %d | %d | %+d |\n", base.Successful, ft.Successful, ft.Successful-base.Successful)
	fmt.Printf("| Failed transactions | %d | %d | %+d |\n", base.Failed, ft.Failed, ft.Failed-base.Failed)
	fmt.Printf("| Lost amount (KZT) | %d | %d | %+d |\n", base.LostAmount, ft.LostAmount, ft.LostAmount-base.LostAmount)
	fmt.Printf("| Retries | %d | %d | %+d |\n", base.Retries, ft.Retries, ft.Retries-base.Retries)
	fmt.Printf("| Rollbacks | %d | %d | %+d |\n", base.Rollbacks, ft.Rollbacks, ft.Rollbacks-base.Rollbacks)
	fmt.Printf("| Completion rate | %s | %s | %+.2f pp |\n",
		pct(base.CompletionRate()), pct(ft.CompletionRate()), ft.CompletionRate()-base.CompletionRate())
	fmt.Println()

	fmt.Println("### Calculations")
	fmt.Println()
	fmt.Printf("- Recovery improvement (completion rate): %s → %s = %+.2f percentage points\n",
		pct(base.CompletionRate()), pct(ft.CompletionRate()), ft.CompletionRate()-base.CompletionRate())
	fmt.Printf("- Transaction-loss reduction (count): (%d − %d) / %d = %s\n",
		base.Failed, ft.Failed, base.Failed, pct(float64(base.Failed-ft.Failed)/float64(base.Failed)*100))
	fmt.Printf("- Transaction-loss reduction (amount): (%d − %d) / %d = %s\n",
		base.LostAmount, ft.LostAmount, base.LostAmount, pct(float64(base.LostAmount-ft.LostAmount)/float64(base.LostAmount)*100))
	fmt.Println()

	fmt.Println("### Contribution by mechanism")
	fmt.Println()
	fmt.Println("| Stage | Successful | Processed amount (KZT) | Added vs previous stage |")
	fmt.Println("|---|---|---|---|")
	fmt.Printf("| A: Baseline | %d | %d | — |\n", base.Successful, base.ProcessedAmount)
	fmt.Printf("| B: + Exception handling | %d | %d | %+d tx, %+d KZT |\n",
		exc.Successful, exc.ProcessedAmount, exc.Successful-base.Successful, exc.ProcessedAmount-base.ProcessedAmount)
	fmt.Printf("| C+D: + Retry, checkpoint, rollback | %d | %d | %+d tx, %+d KZT |\n",
		ft.Successful, ft.ProcessedAmount, ft.Successful-exc.Successful, ft.ProcessedAmount-exc.ProcessedAmount)
	fmt.Println()
	fmt.Println("Logs: logs/baseline.jsonl, logs/exception_handling.jsonl, logs/fault_tolerant.jsonl")
}
