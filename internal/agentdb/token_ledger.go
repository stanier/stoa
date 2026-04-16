package agentdb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// RecordTokens inserts one token usage record. No-op if userID is empty.
func (db *DB) RecordTokens(ctx context.Context, userID, sessionID, provider, model string, input, output int) error {
	if userID == "" {
		return nil
	}
	_, err := db.pool.Exec(ctx,
		`INSERT INTO token_usage (user_id, session_id, provider, model, input_tokens, output_tokens)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		userID, sessionID, provider, model, input, output,
	)
	if err != nil {
		return fmt.Errorf("agentdb: record tokens: %w", err)
	}
	return nil
}

// TokenBudget holds the limits fetched from the token_budgets table.
type TokenBudget struct {
	DailyInputLimit    *int64
	DailyOutputLimit   *int64
	MonthlyInputLimit  *int64
	MonthlyOutputLimit *int64
}

// CheckBudget returns (true, "", nil) if the user has no budget row or is within limits.
// Returns (false, reason, nil) if a budget is exceeded.
// Returns (true, "", err) on DB error (fail open — don't block on DB errors).
func (db *DB) CheckBudget(ctx context.Context, userID string) (ok bool, reason string, err error) {
	var b TokenBudget
	scanErr := db.pool.QueryRow(ctx,
		`SELECT daily_input_limit, daily_output_limit, monthly_input_limit, monthly_output_limit
		 FROM token_budgets WHERE user_id = $1`,
		userID,
	).Scan(&b.DailyInputLimit, &b.DailyOutputLimit, &b.MonthlyInputLimit, &b.MonthlyOutputLimit)
	if errors.Is(scanErr, pgx.ErrNoRows) {
		return true, "", nil
	}
	if scanErr != nil {
		// Fail open on DB error.
		return true, "", fmt.Errorf("agentdb: check budget query: %w", scanErr)
	}

	// No limits set at all.
	if b.DailyInputLimit == nil && b.DailyOutputLimit == nil && b.MonthlyInputLimit == nil && b.MonthlyOutputLimit == nil {
		return true, "", nil
	}

	now := time.Now().UTC()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	// Query daily usage.
	if b.DailyInputLimit != nil || b.DailyOutputLimit != nil {
		var dailyIn, dailyOut int64
		err2 := db.pool.QueryRow(ctx,
			`SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0)
			 FROM token_usage WHERE user_id = $1 AND recorded_at >= $2`,
			userID, todayStart,
		).Scan(&dailyIn, &dailyOut)
		if err2 != nil {
			return true, "", fmt.Errorf("agentdb: check budget daily usage: %w", err2)
		}
		if b.DailyInputLimit != nil && dailyIn >= *b.DailyInputLimit {
			return false, fmt.Sprintf("daily input token limit exceeded (%d/%d)", dailyIn, *b.DailyInputLimit), nil
		}
		if b.DailyOutputLimit != nil && dailyOut >= *b.DailyOutputLimit {
			return false, fmt.Sprintf("daily output token limit exceeded (%d/%d)", dailyOut, *b.DailyOutputLimit), nil
		}
	}

	// Query monthly usage.
	if b.MonthlyInputLimit != nil || b.MonthlyOutputLimit != nil {
		var monthlyIn, monthlyOut int64
		err2 := db.pool.QueryRow(ctx,
			`SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0)
			 FROM token_usage WHERE user_id = $1 AND recorded_at >= $2`,
			userID, monthStart,
		).Scan(&monthlyIn, &monthlyOut)
		if err2 != nil {
			return true, "", fmt.Errorf("agentdb: check budget monthly usage: %w", err2)
		}
		if b.MonthlyInputLimit != nil && monthlyIn >= *b.MonthlyInputLimit {
			return false, fmt.Sprintf("monthly input token limit exceeded (%d/%d)", monthlyIn, *b.MonthlyInputLimit), nil
		}
		if b.MonthlyOutputLimit != nil && monthlyOut >= *b.MonthlyOutputLimit {
			return false, fmt.Sprintf("monthly output token limit exceeded (%d/%d)", monthlyOut, *b.MonthlyOutputLimit), nil
		}
	}

	return true, "", nil
}

// SetBudget upserts the token budget for a user.
func (db *DB) SetBudget(ctx context.Context, userID string, dailyInput, dailyOutput, monthlyInput, monthlyOutput *int64) error {
	_, err := db.pool.Exec(ctx,
		`INSERT INTO token_budgets (user_id, daily_input_limit, daily_output_limit, monthly_input_limit, monthly_output_limit, updated_at)
		 VALUES ($1, $2, $3, $4, $5, now())
		 ON CONFLICT (user_id) DO UPDATE SET
		   daily_input_limit    = EXCLUDED.daily_input_limit,
		   daily_output_limit   = EXCLUDED.daily_output_limit,
		   monthly_input_limit  = EXCLUDED.monthly_input_limit,
		   monthly_output_limit = EXCLUDED.monthly_output_limit,
		   updated_at           = now()`,
		userID, dailyInput, dailyOutput, monthlyInput, monthlyOutput,
	)
	if err != nil {
		return fmt.Errorf("agentdb: set budget: %w", err)
	}
	return nil
}

// GetBudget returns the budget row for a user plus today's and this month's usage.
// Returns nil budget if no row exists.
func (db *DB) GetBudget(ctx context.Context, userID string) (budget *TokenBudget, dailyIn, dailyOut, monthlyIn, monthlyOut int64, err error) {
	var b TokenBudget
	scanErr := db.pool.QueryRow(ctx,
		`SELECT daily_input_limit, daily_output_limit, monthly_input_limit, monthly_output_limit
		 FROM token_budgets WHERE user_id = $1`,
		userID,
	).Scan(&b.DailyInputLimit, &b.DailyOutputLimit, &b.MonthlyInputLimit, &b.MonthlyOutputLimit)
	if errors.Is(scanErr, pgx.ErrNoRows) {
		budget = nil
	} else if scanErr != nil {
		err = fmt.Errorf("agentdb: get budget: %w", scanErr)
		return
	} else {
		budget = &b
	}

	now := time.Now().UTC()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	err = db.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0)
		 FROM token_usage WHERE user_id = $1 AND recorded_at >= $2`,
		userID, todayStart,
	).Scan(&dailyIn, &dailyOut)
	if err != nil {
		err = fmt.Errorf("agentdb: get budget daily usage: %w", err)
		return
	}

	err = db.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0)
		 FROM token_usage WHERE user_id = $1 AND recorded_at >= $2`,
		userID, monthStart,
	).Scan(&monthlyIn, &monthlyOut)
	if err != nil {
		err = fmt.Errorf("agentdb: get budget monthly usage: %w", err)
	}
	return
}

// UsageSummary holds today's and this month's token usage plus budget limits.
type UsageSummary struct {
	TodayInputTokens   int64  `json:"today_input_tokens"`
	TodayOutputTokens  int64  `json:"today_output_tokens"`
	MonthInputTokens   int64  `json:"month_input_tokens"`
	MonthOutputTokens  int64  `json:"month_output_tokens"`
	DailyInputLimit    *int64 `json:"daily_input_limit"`
	DailyOutputLimit   *int64 `json:"daily_output_limit"`
	MonthlyInputLimit  *int64 `json:"monthly_input_limit"`
	MonthlyOutputLimit *int64 `json:"monthly_output_limit"`
}

// GetUsageSummary returns today's and this month's token usage plus budget limits for a user.
// Returns a zero-value summary (not an error) if db is nil or userID is empty.
func (db *DB) GetUsageSummary(ctx context.Context, userID string) (UsageSummary, error) {
	if db == nil || userID == "" {
		return UsageSummary{}, nil
	}

	now := time.Now().UTC()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	var s UsageSummary

	// Today's usage.
	if err := db.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0)
		 FROM token_usage WHERE user_id = $1 AND recorded_at >= $2`,
		userID, todayStart,
	).Scan(&s.TodayInputTokens, &s.TodayOutputTokens); err != nil {
		return s, fmt.Errorf("agentdb: usage summary daily: %w", err)
	}

	// This month's usage.
	if err := db.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0)
		 FROM token_usage WHERE user_id = $1 AND recorded_at >= $2`,
		userID, monthStart,
	).Scan(&s.MonthInputTokens, &s.MonthOutputTokens); err != nil {
		return s, fmt.Errorf("agentdb: usage summary monthly: %w", err)
	}

	// Budget limits (optional — no row is fine).
	scanErr := db.pool.QueryRow(ctx,
		`SELECT daily_input_limit, daily_output_limit, monthly_input_limit, monthly_output_limit
		 FROM token_budgets WHERE user_id = $1`,
		userID,
	).Scan(&s.DailyInputLimit, &s.DailyOutputLimit, &s.MonthlyInputLimit, &s.MonthlyOutputLimit)
	if scanErr != nil && !errors.Is(scanErr, pgx.ErrNoRows) {
		return s, fmt.Errorf("agentdb: usage summary budget: %w", scanErr)
	}

	return s, nil
}

// DeleteBudget removes the budget row for a user, restoring them to unlimited.
func (db *DB) DeleteBudget(ctx context.Context, userID string) error {
	_, err := db.pool.Exec(ctx, `DELETE FROM token_budgets WHERE user_id = $1`, userID)
	if err != nil {
		return fmt.Errorf("agentdb: delete budget: %w", err)
	}
	return nil
}
