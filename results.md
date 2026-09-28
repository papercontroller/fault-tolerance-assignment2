## Part A — Baseline

| Metric | Result |
|---|---|
| Transactions attempted | 2 |
| Successful transactions | 1 |
| Transactions lost | 14 |
| Processed amount (KZT) | 12000 |
| Lost amount (KZT) | 425000 |

## Part B — Exception Handling

| Transaction | Failure | Exception | Recovery Action | Final Status |
|---|---|---|---|---|
| T002 | Network | NetworkError | Logged, skipped, continued | FAILED |
| T004 | Timeout | TimeoutError | Logged, skipped, continued | FAILED |
| T006 | Database | DatabaseError | Logged, skipped, continued | FAILED |
| T008 | Network | NetworkError | Logged, skipped, continued | FAILED |
| T010 | Timeout | TimeoutError | Logged, skipped, continued | FAILED |
| T012 | Database | DatabaseError | Logged, skipped, continued | FAILED |
| T015 | Network | NetworkError | Logged, skipped, continued | FAILED |

## Part C — Retry Attempt Log (transactions with failures)

| Step / Timestamp | Transaction | Attempt | Failure | Action | Result |
|---|---|---|---|---|---|
| 2 / 16:32:59.009 | T002 | Initial | Network | Retry after 1s | FAIL |
| 3 / 16:33:00.010 | T002 | Retry 1 | Network | Record | SUCCESS |
| 5 / 16:33:01.262 | T004 | Initial | Timeout | Retry after 1s | FAIL |
| 6 / 16:33:02.270 | T004 | Retry 1 | Timeout | Retry after 2s | FAIL |
| 7 / 16:33:04.270 | T004 | Retry 2 | Timeout | Record | SUCCESS |
| 9 / 16:33:04.270 | T006 | Initial | Database | Retry after 1s | FAIL |
| 10 / 16:33:05.271 | T006 | Retry 1 | Database | Retry after 2s | FAIL |
| 11 / 16:33:07.272 | T006 | Retry 2 | Database | Rollback to CP1 | ROLLBACK |
| 13 / 16:33:07.274 | T008 | Initial | Network | Retry after 1s | FAIL |
| 14 / 16:33:08.275 | T008 | Retry 1 | Network | Record | SUCCESS |
| 16 / 16:33:08.275 | T010 | Initial | Timeout | Retry after 1s | FAIL |
| 17 / 16:33:09.277 | T010 | Retry 1 | Timeout | Retry after 2s | FAIL |
| 18 / 16:33:11.277 | T010 | Retry 2 | Timeout | Record | SUCCESS |
| 20 / 16:33:11.278 | T012 | Initial | Database | Retry after 1s | FAIL |
| 21 / 16:33:12.279 | T012 | Retry 1 | Database | Retry after 2s | FAIL |
| 22 / 16:33:14.280 | T012 | Retry 2 | Database | Rollback to CP2 | ROLLBACK |
| 25 / 16:33:14.280 | T015 | Initial | Network | Retry after 1s | FAIL |
| 26 / 16:33:15.281 | T015 | Retry 1 | Network | Record | SUCCESS |

## Part D — Checkpoints

| Checkpoint | Successful transactions | Total amount (KZT) | State saved |
|---|---|---|---|
| CP1 | 5 | 103000 | T001, T002, T003, T004, T005 |
| CP2 | 10 | 214000 | T001, T002, T003, T004, T005, T007, T008, T009, T010, T011 |
| CP3 | — | — | Not created: only 13 successful transactions, 15 needed |

## Part E — Before/After

| Metric | Baseline | Fault-tolerant | Improvement |
|---|---|---|---|
| Successful transactions | 1 | 13 | +12 |
| Failed transactions | 14 | 2 | -12 |
| Lost amount (KZT) | 425000 | 150000 | -275000 |
| Retries | 0 | 11 | +11 |
| Rollbacks | 0 | 2 | +2 |
| Completion rate | 6.67% | 86.67% | +80.00 pp |

### Calculations

- Recovery improvement (completion rate): 6.67% → 86.67% = +80.00 percentage points
- Transaction-loss reduction (count): (14 − 2) / 14 = 85.71%
- Transaction-loss reduction (amount): (425000 − 150000) / 425000 = 64.71%

### Contribution by mechanism

| Stage | Successful | Processed amount (KZT) | Added vs previous stage |
|---|---|---|---|
| A: Baseline | 1 | 12000 | — |
| B: + Exception handling | 8 | 96000 | +7 tx, +84000 KZT |
| C+D: + Retry, checkpoint, rollback | 13 | 287000 | +5 tx, +191000 KZT |

Logs: logs/baseline.jsonl, logs/exception_handling.jsonl, logs/fault_tolerant.jsonl
