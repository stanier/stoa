// auth-admin is the administrative CLI for the stoa auth system.
//
// It manages database schema migrations and user/session records for both
// authd and local-idp. It always operates directly against Postgres using
// DDL-capable credentials — it does not require authd or local-idp to be running.
//
// Usage:
//
//	auth-admin authd migrate [up|down|status|version]
//	auth-admin authd user list
//	auth-admin authd user revoke <email>
//	auth-admin authd service create <id> [--ttl-hours N]
//
//	auth-admin idp migrate [up|down|status]
//	auth-admin idp user list
//	auth-admin idp user create --username U --email E [--name N] --password P
//	auth-admin idp user reset-password --username U --password P
//	auth-admin idp user unlock --username U
//	auth-admin idp user delete --username U
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"

	"github.com/stanier/stoa/internal/agentdb"
	"github.com/stanier/stoa/internal/agents/personas"
	"github.com/stanier/stoa/internal/authdb"
	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/localidp"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() { <-sigCh; cancel() }()

	switch os.Args[1] {
	case "authd":
		runAuthdCmd(ctx, os.Args[2:])
	case "idp":
		runIDPCmd(ctx, os.Args[2:])
	case "agentd":
		runAgentdCmd(ctx, os.Args[2:])
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "auth-admin: unknown subcommand %q\n\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

// ── authd subcommands ─────────────────────────────────────────────────────────

func runAuthdCmd(ctx context.Context, args []string) {
	if len(args) == 0 {
		usageAuthd()
		os.Exit(1)
	}
	switch args[0] {
	case "migrate":
		runAuthdMigrate(ctx, args[1:])
	case "user":
		runAuthdUser(ctx, args[1:])
	case "service":
		runAuthdService(ctx, args[1:])
	case "role":
		runAuthdRole(ctx, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "auth-admin authd: unknown subcommand %q\n\n", args[0])
		usageAuthd()
		os.Exit(1)
	}
}

func runAuthdMigrate(ctx context.Context, args []string) {
	cmd := "up"
	if len(args) > 0 {
		cmd = args[0]
	}

	if cmd == "version" {
		fmt.Printf("required schema version: %d\n", authdb.RequiredSchemaVersion)
		return
	}

	dsn := requireEnv("AUTHD_MIGRATE_DATABASE_URL", "AGENTD_MIGRATE_DATABASE_URL")
	var err error
	switch cmd {
	case "up":
		fmt.Println("auth-admin: authd: applying migrations...")
		err = authdb.Migrate(ctx, dsn)
		if err == nil {
			fmt.Println("auth-admin: authd: migrations applied")
		}
	case "down":
		fmt.Println("auth-admin: authd: rolling back last migration...")
		err = authdb.MigrateDown(ctx, dsn)
		if err == nil {
			fmt.Println("auth-admin: authd: rollback complete")
		}
	case "status":
		err = authdb.MigrateStatus(ctx, dsn)
	default:
		fmt.Fprintf(os.Stderr, "auth-admin authd migrate: unknown command %q\n", cmd)
		os.Exit(1)
	}
	fatalIf(err)
}

func runAuthdUser(ctx context.Context, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: auth-admin authd user [list|revoke]")
		os.Exit(1)
	}

	dsn := requireEnv("AUTHD_MIGRATE_DATABASE_URL", "AGENTD_MIGRATE_DATABASE_URL")
	db, err := authdb.Open(ctx, dsn)
	fatalIf(err)
	defer db.Close()

	switch args[0] {
	case "list":
		users, err := db.ListUsersWithRoles(ctx)
		fatalIf(err)
		if len(users) == 0 {
			fmt.Println("(no users)")
			return
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tEMAIL\tDISPLAY NAME\tPROVIDER\tROLE\tLAST SEEN")
		for _, u := range users {
			lastSeen := "never"
			if u.LastSeenAt != nil {
				lastSeen = u.LastSeenAt.Format(time.RFC3339)
			}
			role := u.RoleName
			if role == "" {
				role = "(none)"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
				u.ID, u.Email, u.DisplayName, u.ProviderID, role, lastSeen)
		}
		w.Flush()

	case "revoke":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: auth-admin authd user revoke <email>")
			os.Exit(1)
		}
		n, err := db.RevokeAllSessionsForEmail(ctx, args[1])
		fatalIf(err)
		fmt.Printf("auth-admin: revoked %d session(s) for %s\n", n, args[1])

	default:
		fmt.Fprintf(os.Stderr, "auth-admin authd user: unknown command %q\n", args[0])
		os.Exit(1)
	}
}

func runAuthdService(ctx context.Context, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: auth-admin authd service create <id> [--ttl-hours N]")
		os.Exit(1)
	}

	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("service create", flag.ExitOnError)
		ttlHours := fs.Int("ttl-hours", 8760, "Token TTL in hours (default: 8760 = 1 year)")
		fatalIf(fs.Parse(args[1:]))
		if fs.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "Usage: auth-admin authd service create <id> [--ttl-hours N]")
			os.Exit(1)
		}
		serviceID := fs.Arg(0)

		dsn := requireEnv("AUTHD_MIGRATE_DATABASE_URL", "AGENTD_MIGRATE_DATABASE_URL")
		db, err := authdb.Open(ctx, dsn)
		fatalIf(err)
		defer db.Close()

		userID, err := db.UpsertUser(ctx, "service", serviceID, serviceID+"@service", serviceID)
		fatalIf(err)

		ttl := time.Duration(*ttlHours) * time.Hour
		rawToken, sess, err := db.CreateSession(ctx, userID, ttl)
		fatalIf(err)

		fmt.Printf("auth-admin: service token for %q (expires %s):\n\n%s\n\n",
			serviceID, sess.ExpiresAt.Format(time.RFC3339), rawToken)
		fmt.Println("Set this as AUTHD_SERVICE_TOKEN (or equivalent) in the service's environment.")

	default:
		fmt.Fprintf(os.Stderr, "auth-admin authd service: unknown command %q\n", args[0])
		os.Exit(1)
	}
}

// ── authd role subcommands ────────────────────────────────────────────────────

func runAuthdRole(ctx context.Context, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: auth-admin authd role [list|create|delete|assign|remove]")
		os.Exit(1)
	}

	dsn := requireEnv("AUTHD_MIGRATE_DATABASE_URL", "AGENTD_MIGRATE_DATABASE_URL")
	db, err := authdb.Open(ctx, dsn)
	fatalIf(err)
	defer db.Close()

	switch args[0] {
	case "list":
		roles, err := db.ListRoles(ctx)
		fatalIf(err)
		if len(roles) == 0 {
			fmt.Println("(no roles)")
			return
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tPERSONA\tALLOWED TOOLS\tCREATED")
		for _, r := range roles {
			tools := "(all)"
			if len(r.AllowedTools) > 0 {
				tools = strings.Join(r.AllowedTools, ", ")
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				r.ID, r.Name, r.Persona, tools, r.CreatedAt.Format(time.RFC3339))
		}
		w.Flush()

	case "create":
		fs := flag.NewFlagSet("role create", flag.ExitOnError)
		persona := fs.String("persona", "", "Persona to use for this role (optional)")
		tools := fs.String("allowed-tools", "", "Comma-separated glob patterns for allowed tools (optional, empty = all)")
		fatalIf(fs.Parse(args[1:]))
		if fs.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "Usage: auth-admin authd role create <name> [--persona P] [--allowed-tools 'pat1,pat2']")
			os.Exit(1)
		}
		name := fs.Arg(0)
		var toolList []string
		if *tools != "" {
			for _, t := range strings.Split(*tools, ",") {
				if t = strings.TrimSpace(t); t != "" {
					toolList = append(toolList, t)
				}
			}
		}
		id, err := db.CreateRole(ctx, name, *persona, toolList)
		fatalIf(err)
		fmt.Printf("auth-admin: created role %q (id: %s)\n", name, id)

	case "delete":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: auth-admin authd role delete <name>")
			os.Exit(1)
		}
		fatalIf(db.DeleteRole(ctx, args[1]))
		fmt.Printf("auth-admin: deleted role %q\n", args[1])

	case "assign":
		// assign <user-email> <role-name>
		fs := flag.NewFlagSet("role assign", flag.ExitOnError)
		fatalIf(fs.Parse(args[1:]))
		if fs.NArg() < 2 {
			fmt.Fprintln(os.Stderr, "Usage: auth-admin authd role assign <user-email> <role-name>")
			os.Exit(1)
		}
		userEmail, roleName := fs.Arg(0), fs.Arg(1)
		users, err := db.ListUsers(ctx)
		fatalIf(err)
		var userID string
		for _, u := range users {
			if u.Email == userEmail {
				userID = u.ID
				break
			}
		}
		if userID == "" {
			fmt.Fprintf(os.Stderr, "auth-admin: no user found with email %q\n", userEmail)
			os.Exit(1)
		}
		fatalIf(db.AssignRole(ctx, userID, roleName))
		fmt.Printf("auth-admin: assigned role %q to %s\n", roleName, userEmail)

	case "remove":
		// remove <user-email>
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: auth-admin authd role remove <user-email>")
			os.Exit(1)
		}
		userEmail := args[1]
		users, err := db.ListUsers(ctx)
		fatalIf(err)
		var userID string
		for _, u := range users {
			if u.Email == userEmail {
				userID = u.ID
				break
			}
		}
		if userID == "" {
			fmt.Fprintf(os.Stderr, "auth-admin: no user found with email %q\n", userEmail)
			os.Exit(1)
		}
		fatalIf(db.RemoveRole(ctx, userID))
		fmt.Printf("auth-admin: removed role assignment for %s\n", userEmail)

	default:
		fmt.Fprintf(os.Stderr, "auth-admin authd role: unknown command %q\n", args[0])
		os.Exit(1)
	}
}

// ── agentd subcommands ─────────────────────────────────────────────────────────

func runAgentdCmd(ctx context.Context, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: auth-admin agentd [migrate|agent|budget] ...")
		os.Exit(1)
	}
	switch args[0] {
	case "migrate":
		runAgentdMigrate(ctx, args[1:])
	case "agent":
		runAgentdAgent(ctx, args[1:])
	case "budget":
		runAgentdBudget(ctx, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "auth-admin agentd: unknown subcommand %q\n", args[0])
		os.Exit(1)
	}
}

func runAgentdAgent(ctx context.Context, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: auth-admin agentd agent [list|create|publish|unpublish|delete]")
		os.Exit(1)
	}

	switch args[0] {
	case "list":
		dsn := requireEnv("AGENTD_MIGRATE_DATABASE_URL", "AGENTD_DATABASE_URL")
		db, err := agentdb.Open(ctx, dsn)
		fatalIf(err)
		defer db.Close()

		agents, err := db.ListAgents(ctx, "", false)
		fatalIf(err)
		if len(agents) == 0 {
			fmt.Println("(no agents)")
			return
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tDISPLAY NAME\tVISIBILITY\tPUBLISHED\tEXTERNAL URL")
		for _, a := range agents {
			published := "unpublished"
			if a.PublishedAt != nil {
				published = a.PublishedAt.Format(time.RFC3339)
			}
			extURL := a.ExternalURL
			if len(extURL) > 40 {
				extURL = extURL[:37] + "..."
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				a.Name, a.DisplayName, string(a.Visibility), published, extURL)
		}
		w.Flush()

	case "create":
		fs := flag.NewFlagSet("agentd agent create", flag.ExitOnError)
		name := fs.String("name", "", "Slug name for the agent (required)")
		displayName := fs.String("display-name", "", "Human-readable display name (required)")
		description := fs.String("description", "", "Short description")
		systemPromptFile := fs.String("system-prompt-file", "", "Path to file containing the system prompt")
		visibility := fs.String("visibility", "internal", "Visibility: internal or external")
		externalURL := fs.String("external-url", "", "URL of external ACP agent to proxy to (optional)")
		fatalIf(fs.Parse(args[1:]))

		if *name == "" || *displayName == "" {
			fmt.Fprintln(os.Stderr, "auth-admin agentd agent create: --name and --display-name are required")
			os.Exit(1)
		}
		if *visibility != "internal" && *visibility != "external" {
			fmt.Fprintln(os.Stderr, "auth-admin agentd agent create: --visibility must be internal or external")
			os.Exit(1)
		}

		var systemPrompt string
		if *systemPromptFile != "" {
			data, err := os.ReadFile(*systemPromptFile)
			fatalIf(err)
			systemPrompt = strings.TrimRight(string(data), "\n")
		}

		dsn := requireEnv("AGENTD_MIGRATE_DATABASE_URL", "AGENTD_DATABASE_URL")
		db, err := agentdb.Open(ctx, dsn)
		fatalIf(err)
		defer db.Close()

		a := agentdb.Agent{
			ID:           uuid.New().String(),
			Name:         *name,
			DisplayName:  *displayName,
			Description:  *description,
			SystemPrompt: systemPrompt,
			Visibility:   agentdb.AgentVisibility(*visibility),
			ExternalURL:  *externalURL,
		}
		fatalIf(db.CreateAgent(ctx, a))
		fmt.Printf("auth-admin: created agent %q (id: %s)\n", *name, a.ID)

	case "publish":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: auth-admin agentd agent publish <name>")
			os.Exit(1)
		}
		dsn := requireEnv("AGENTD_MIGRATE_DATABASE_URL", "AGENTD_DATABASE_URL")
		db, err := agentdb.Open(ctx, dsn)
		fatalIf(err)
		defer db.Close()
		fatalIf(db.PublishAgent(ctx, args[1]))
		fmt.Printf("auth-admin: published agent %q\n", args[1])

	case "unpublish":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: auth-admin agentd agent unpublish <name>")
			os.Exit(1)
		}
		dsn := requireEnv("AGENTD_MIGRATE_DATABASE_URL", "AGENTD_DATABASE_URL")
		db, err := agentdb.Open(ctx, dsn)
		fatalIf(err)
		defer db.Close()
		fatalIf(db.UnpublishAgent(ctx, args[1]))
		fmt.Printf("auth-admin: unpublished agent %q\n", args[1])

	case "delete":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: auth-admin agentd agent delete <name>")
			os.Exit(1)
		}
		dsn := requireEnv("AGENTD_MIGRATE_DATABASE_URL", "AGENTD_DATABASE_URL")
		db, err := agentdb.Open(ctx, dsn)
		fatalIf(err)
		defer db.Close()
		fatalIf(db.DeleteAgent(ctx, args[1]))
		fmt.Printf("auth-admin: deleted agent %q\n", args[1])

	case "seed":
		dsn := requireEnv("AGENTD_MIGRATE_DATABASE_URL", "AGENTD_DATABASE_URL")
		db, err := agentdb.Open(ctx, dsn)
		fatalIf(err)
		defer db.Close()

		configDir, err := config.GetConfigDir()
		fatalIf(err)

		reg := personas.NewRegistry()
		if err := reg.LoadFromConfig(configDir); err != nil {
			fmt.Fprintf(os.Stderr, "auth-admin: load personas: %v\n", err)
			os.Exit(1)
		}

		all := reg.ListPersonas()
		if len(all) == 0 {
			fmt.Println("auth-admin: no personas found in config directory")
			return
		}

		n, err := db.SeedAgentsFromPersonas(ctx, all)
		fatalIf(err)
		alreadyExisted := len(all) - n
		fmt.Printf("auth-admin: seeded %d agent(s) from config personas (%d already existed)\n", n, alreadyExisted)

	default:
		fmt.Fprintf(os.Stderr, "auth-admin agentd agent: unknown command %q\n", args[0])
		os.Exit(1)
	}
}

func runAgentdBudget(ctx context.Context, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: auth-admin agentd budget [set|get|reset]")
		os.Exit(1)
	}
	switch args[0] {
	case "set":
		fs := flag.NewFlagSet("budget set", flag.ExitOnError)
		user := fs.String("user", "", "User ID (required)")
		dailyInput := fs.Int64("daily-input", -1, "Daily input token limit (-1 = no limit)")
		dailyOutput := fs.Int64("daily-output", -1, "Daily output token limit (-1 = no limit)")
		monthlyInput := fs.Int64("monthly-input", -1, "Monthly input token limit (-1 = no limit)")
		monthlyOutput := fs.Int64("monthly-output", -1, "Monthly output token limit (-1 = no limit)")
		fatalIf(fs.Parse(args[1:]))
		if *user == "" {
			fmt.Fprintln(os.Stderr, "auth-admin agentd budget set: --user is required")
			os.Exit(1)
		}
		dsn := requireEnv("AGENTD_MIGRATE_DATABASE_URL", "AGENTD_DATABASE_URL")
		db, err := agentdb.Open(ctx, dsn)
		fatalIf(err)
		defer db.Close()

		var di, dout, mi, mout *int64
		if *dailyInput >= 0 {
			di = dailyInput
		}
		if *dailyOutput >= 0 {
			dout = dailyOutput
		}
		if *monthlyInput >= 0 {
			mi = monthlyInput
		}
		if *monthlyOutput >= 0 {
			mout = monthlyOutput
		}
		fatalIf(db.SetBudget(ctx, *user, di, dout, mi, mout))
		fmt.Printf("auth-admin: budget set for user %s\n", *user)

	case "get":
		fs := flag.NewFlagSet("budget get", flag.ExitOnError)
		user := fs.String("user", "", "User ID (required)")
		fatalIf(fs.Parse(args[1:]))
		if *user == "" {
			fmt.Fprintln(os.Stderr, "auth-admin agentd budget get: --user is required")
			os.Exit(1)
		}
		dsn := requireEnv("AGENTD_MIGRATE_DATABASE_URL", "AGENTD_DATABASE_URL")
		db, err := agentdb.Open(ctx, dsn)
		fatalIf(err)
		defer db.Close()

		budget, dailyIn, dailyOut, monthlyIn, monthlyOut, err := db.GetBudget(ctx, *user)
		fatalIf(err)

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "User:\t%s\n", *user)
		printLimit := func(label string, limit *int64) {
			if limit == nil {
				fmt.Fprintf(w, "%s:\tunlimited\n", label)
			} else {
				fmt.Fprintf(w, "%s:\t%d\n", label, *limit)
			}
		}
		if budget == nil {
			fmt.Fprintln(w, "Limits:\tunlimited (no budget row)")
		} else {
			printLimit("Daily input limit", budget.DailyInputLimit)
			printLimit("Daily output limit", budget.DailyOutputLimit)
			printLimit("Monthly input limit", budget.MonthlyInputLimit)
			printLimit("Monthly output limit", budget.MonthlyOutputLimit)
		}
		fmt.Fprintf(w, "Today input used:\t%d\n", dailyIn)
		fmt.Fprintf(w, "Today output used:\t%d\n", dailyOut)
		fmt.Fprintf(w, "Month input used:\t%d\n", monthlyIn)
		fmt.Fprintf(w, "Month output used:\t%d\n", monthlyOut)
		w.Flush()

	case "reset":
		fs := flag.NewFlagSet("budget reset", flag.ExitOnError)
		user := fs.String("user", "", "User ID (required)")
		fatalIf(fs.Parse(args[1:]))
		if *user == "" {
			fmt.Fprintln(os.Stderr, "auth-admin agentd budget reset: --user is required")
			os.Exit(1)
		}
		dsn := requireEnv("AGENTD_MIGRATE_DATABASE_URL", "AGENTD_DATABASE_URL")
		db, err := agentdb.Open(ctx, dsn)
		fatalIf(err)
		defer db.Close()
		fatalIf(db.DeleteBudget(ctx, *user))
		fmt.Printf("auth-admin: budget reset (unlimited) for user %s\n", *user)

	default:
		fmt.Fprintf(os.Stderr, "auth-admin agentd budget: unknown command %q\n", args[0])
		os.Exit(1)
	}
}

func runAgentdMigrate(ctx context.Context, args []string) {
	cmd := "up"
	if len(args) > 0 {
		cmd = args[0]
	}
	dsn := requireEnv("AGENTD_MIGRATE_DATABASE_URL", "AGENTD_DATABASE_URL")
	var err error
	switch cmd {
	case "up":
		fmt.Println("auth-admin: agentd: applying migrations...")
		err = agentdb.Migrate(ctx, dsn)
		if err == nil {
			fmt.Println("auth-admin: agentd: migrations applied")
		}
	case "down":
		fmt.Println("auth-admin: agentd: rolling back last migration...")
		err = agentdb.MigrateDown(ctx, dsn)
		if err == nil {
			fmt.Println("auth-admin: agentd: rollback complete")
		}
	case "status":
		err = agentdb.MigrateStatus(ctx, dsn)
	default:
		fmt.Fprintf(os.Stderr, "auth-admin agentd migrate: unknown command %q\n", cmd)
		os.Exit(1)
	}
	fatalIf(err)
}

// ── idp subcommands ────────────────────────────────────────────────────────────

func runIDPCmd(ctx context.Context, args []string) {
	if len(args) == 0 {
		usageIDP()
		os.Exit(1)
	}
	switch args[0] {
	case "migrate":
		runIDPMigrate(ctx, args[1:])
	case "user":
		runIDPUser(ctx, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "auth-admin idp: unknown subcommand %q\n\n", args[0])
		usageIDP()
		os.Exit(1)
	}
}

func runIDPMigrate(ctx context.Context, args []string) {
	cmd := "up"
	if len(args) > 0 {
		cmd = args[0]
	}

	dsn := requireEnv("LOCAL_IDP_MIGRATE_DATABASE_URL", "LOCAL_IDP_DATABASE_URL")
	var err error
	switch cmd {
	case "up":
		fmt.Println("auth-admin: idp: applying migrations...")
		err = localidp.Migrate(ctx, dsn)
		if err == nil {
			fmt.Println("auth-admin: idp: migrations applied")
		}
	case "down":
		fmt.Println("auth-admin: idp: rolling back last migration...")
		err = localidp.MigrateDown(ctx, dsn)
		if err == nil {
			fmt.Println("auth-admin: idp: rollback complete")
		}
	case "status":
		err = localidp.MigrateStatus(ctx, dsn)
	default:
		fmt.Fprintf(os.Stderr, "auth-admin idp migrate: unknown command %q\n", cmd)
		os.Exit(1)
	}
	fatalIf(err)
}

func runIDPUser(ctx context.Context, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: auth-admin idp user [list|create|reset-password|unlock|delete]")
		os.Exit(1)
	}

	dsn := requireEnv("LOCAL_IDP_MIGRATE_DATABASE_URL", "LOCAL_IDP_DATABASE_URL")

	switch args[0] {
	case "list":
		users, err := localidp.AdminListUsers(ctx, dsn)
		fatalIf(err)
		if len(users) == 0 {
			fmt.Println("(no users)")
			return
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tUSERNAME\tEMAIL\tDISPLAY NAME\tCREATED")
		for _, u := range users {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				u.ID, u.Username, u.Email, u.DisplayName,
				u.CreatedAt.Format(time.RFC3339))
		}
		w.Flush()

	case "create":
		fs := flag.NewFlagSet("idp user create", flag.ExitOnError)
		username := fs.String("username", "", "Username (required)")
		email := fs.String("email", "", "Email address (required)")
		name := fs.String("name", "", "Display name (defaults to username)")
		password := fs.String("password", "", "Password (required; prefer STDIN prompt in production)")
		fatalIf(fs.Parse(args[1:]))

		if *username == "" || *email == "" || *password == "" {
			fmt.Fprintln(os.Stderr, "auth-admin idp user create: --username, --email, and --password are required")
			os.Exit(1)
		}

		id, err := localidp.AdminCreateUser(ctx, dsn, *username, *email, *name, *password)
		fatalIf(err)
		fmt.Printf("auth-admin: created user %q (id: %s)\n", *username, id)

	case "reset-password":
		fs := flag.NewFlagSet("idp user reset-password", flag.ExitOnError)
		username := fs.String("username", "", "Username (required)")
		password := fs.String("password", "", "New password (required)")
		fatalIf(fs.Parse(args[1:]))

		if *username == "" || *password == "" {
			fmt.Fprintln(os.Stderr, "auth-admin idp user reset-password: --username and --password are required")
			os.Exit(1)
		}
		fatalIf(localidp.AdminResetPassword(ctx, dsn, *username, *password))
		fmt.Printf("auth-admin: password reset for %q\n", *username)

	case "unlock":
		fs := flag.NewFlagSet("idp user unlock", flag.ExitOnError)
		username := fs.String("username", "", "Username (required)")
		fatalIf(fs.Parse(args[1:]))

		if *username == "" {
			fmt.Fprintln(os.Stderr, "auth-admin idp user unlock: --username is required")
			os.Exit(1)
		}
		fatalIf(localidp.AdminUnlockUser(ctx, dsn, *username))
		fmt.Printf("auth-admin: unlocked account %q\n", *username)

	case "delete":
		fs := flag.NewFlagSet("idp user delete", flag.ExitOnError)
		username := fs.String("username", "", "Username (required)")
		fatalIf(fs.Parse(args[1:]))

		if *username == "" {
			fmt.Fprintln(os.Stderr, "auth-admin idp user delete: --username is required")
			os.Exit(1)
		}
		fatalIf(localidp.AdminDeleteUser(ctx, dsn, *username))
		fmt.Printf("auth-admin: deleted user %q\n", *username)

	default:
		fmt.Fprintf(os.Stderr, "auth-admin idp user: unknown command %q\n", args[0])
		os.Exit(1)
	}
}

// ── Helpers ────────────────────────────────────────────────────────────────────

// requireEnv returns the first non-empty env var from the list, or exits.
func requireEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	fmt.Fprintf(os.Stderr, "auth-admin: one of these environment variables is required: %v\n", keys)
	os.Exit(1)
	return ""
}

func fatalIf(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "auth-admin: %v\n", err)
		os.Exit(1)
	}
}

// ── Usage ──────────────────────────────────────────────────────────────────────

func usage() {
	fmt.Print(`auth-admin — stoa authentication system CLI

Usage:
  auth-admin authd  <subcommand>  Manage authd schema and users
  auth-admin idp    <subcommand>  Manage local-idp schema and users
  auth-admin agentd <subcommand>  Manage agentd session database

authd subcommands:
  migrate [up|down|status|version]                     Schema migrations (env: AUTHD_MIGRATE_DATABASE_URL)
  user list                                            List all registered users
  user revoke <email>                                  Revoke all sessions for a user
  service create <id> [--ttl-hours N]                  Issue a long-lived service account token
  role list                                            List all roles
  role create <name> [--persona P] [--allowed-tools T] Create a role
  role delete <name>                                   Delete a role (fails if users assigned)
  role assign <user-email> <role-name>                 Assign a role to a user
  role remove <user-email>                             Remove a user's role assignment

idp subcommands:
  migrate [up|down|status]                 Schema migrations (env: LOCAL_IDP_MIGRATE_DATABASE_URL)
  user list                                List all local accounts
  user create --username U --email E --password P [--name N]
  user reset-password --username U --password P
  user unlock --username U
  user delete --username U

agentd subcommands:
  migrate [up|down|status]                                           Schema migrations (env: AGENTD_MIGRATE_DATABASE_URL)
  agent list                                                         List all agents
  agent create --name <slug> --display-name <name> [options]        Create an agent
  agent publish <name>                                               Publish an agent
  agent unpublish <name>                                             Unpublish an agent
  agent delete <name>                                                Delete an agent
  agent seed                                                         Seed registry from config personas (idempotent)
  budget set --user <id> [--daily-input N] [--daily-output N] [--monthly-input N] [--monthly-output N]
  budget get --user <id>                                             Show limits and current usage
  budget reset --user <id>                                           Delete budget row (restore unlimited)
`)
}

func usageAuthd() {
	fmt.Print(`Usage: auth-admin authd <subcommand>

  migrate [up|down|status|version]
  user list
  user revoke <email>
  service create <id> [--ttl-hours N]
  role list
  role create <name> [--persona P] [--allowed-tools T]
  role delete <name>
  role assign <user-email> <role-name>
  role remove <user-email>

Environment:
  AUTHD_MIGRATE_DATABASE_URL   Postgres DSN with DDL privileges
`)
}

func usageIDP() {
	fmt.Print(`Usage: auth-admin idp <subcommand>

  migrate [up|down|status]
  user list
  user create --username U --email E --password P [--name N]
  user reset-password --username U --password P
  user unlock --username U
  user delete --username U

Environment:
  LOCAL_IDP_MIGRATE_DATABASE_URL   Postgres DSN with DDL privileges
`)
}
