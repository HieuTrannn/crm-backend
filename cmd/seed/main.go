// Command seed prepares demo data for local development and load tests
// (FR-SYS-03). It is idempotent: running it again creates nothing twice.
//
//	go run ./cmd/seed                              # 2 demo tenants with sample data
//	go run ./cmd/seed -users 200 -contacts 100000  # plus load-test accounts and contacts
//
// Every account uses SEED_OWNER_PASSWORD. Accounts are created through the same
// services as the API, so passwords, slugs and invariants match a real sign-up.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/HieuTrannn/crm-backend/db/sqlc"
	"github.com/HieuTrannn/crm-backend/internal/auth"
	"github.com/HieuTrannn/crm-backend/internal/config"
	"github.com/HieuTrannn/crm-backend/internal/contact"
	"github.com/HieuTrannn/crm-backend/internal/deal"
	"github.com/HieuTrannn/crm-backend/internal/platform/database"
)

const minPassword = 8 // FR-AUTH-02

// demoTenant is one FR-SYS-03 trial tenant.
type demoTenant struct {
	company, domain string
}

func (d demoTenant) ownerEmail() string { return "owner@" + d.domain }

// The first tenant also receives the load-test users and contacts.
func demoTenants() []demoTenant {
	return []demoTenant{
		{company: "Acme Demo", domain: "acme.demo"},
		{company: "Beta Demo", domain: "beta.demo"},
	}
}

func main() {
	users := flag.Int("users", 0, "load-test member accounts loadtest-NNN@acme.demo to create")
	contacts := flag.Int("contacts", 0, "load-test contacts to add to Acme Demo (e.g. 100000)")
	flag.Parse()

	if err := run(*users, *contacts); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("seed failed", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(users, contacts int) error {
	cfg, err := config.Load() // also loads .env
	if err != nil {
		return err
	}
	password := os.Getenv("SEED_OWNER_PASSWORD")
	if len(password) < minPassword {
		return fmt.Errorf("SEED_OWNER_PASSWORD must be at least %d characters", minPassword)
	}
	if users < 0 || contacts < 0 || contacts > 1_000_000 {
		return errors.New("-users and -contacts must be >= 0, and -contacts at most 1000000")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	pool, err := database.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer pool.Close()

	s, err := newSeeder(pool, password)
	if err != nil {
		return err
	}
	var acme seededTenant
	for i, t := range demoTenants() {
		st, err := s.tenant(ctx, t)
		if err != nil {
			return fmt.Errorf("tenant %s: %w", t.company, err)
		}
		if i == 0 {
			acme = st
		}
	}
	if users > 0 {
		if err := s.loadTestUsers(ctx, acme, users); err != nil {
			return fmt.Errorf("load-test users: %w", err)
		}
	}
	if contacts > 0 {
		if err := s.bulkContacts(ctx, acme, contacts); err != nil {
			return fmt.Errorf("load-test contacts: %w", err)
		}
	}
	fmt.Println("done; every account uses SEED_OWNER_PASSWORD")
	return nil
}

type seeder struct {
	db       *database.DB
	authRepo *auth.PostgresRepository
	authSvc  *auth.Service
	contacts *contact.Service
	deals    *deal.Service
	password string
}

type seededTenant struct {
	demoTenant
	tenantID, ownerID uuid.UUID
}

func newSeeder(pool *pgxpool.Pool, password string) (*seeder, error) {
	db := database.New(pool)
	authRepo := auth.NewPostgresRepository(db, pool)
	authSvc, err := auth.NewService(authRepo, nil, nil, auth.BcryptCost)
	if err != nil {
		return nil, err
	}
	return &seeder{
		db: db, authRepo: authRepo, authSvc: authSvc, password: password,
		contacts: contact.NewService(contact.NewPostgresRepository(db)),
		deals:    deal.NewService(deal.NewPostgresRepository(db)),
	}, nil
}

// tenant registers the owner, and adds sample data only when the tenant is new.
func (s *seeder) tenant(ctx context.Context, t demoTenant) (seededTenant, error) {
	me, err := s.authSvc.Register(ctx, auth.RegisterInput{
		CompanyName: t.company, Email: t.ownerEmail(), Password: s.password, FullName: "Owner " + t.company,
	})
	if errors.Is(err, auth.ErrEmailTaken) {
		cred, err := s.authRepo.FindUserByEmail(ctx, t.ownerEmail())
		if err != nil {
			return seededTenant{}, err
		}
		fmt.Printf("%s: exists (owner %s)\n", t.company, t.ownerEmail())
		return seededTenant{demoTenant: t, tenantID: cred.TenantID, ownerID: cred.UserID}, nil
	}
	if err != nil {
		return seededTenant{}, err
	}
	st := seededTenant{demoTenant: t, tenantID: me.Tenant.ID, ownerID: me.ID}
	if err := s.sampleData(ctx, st); err != nil {
		return seededTenant{}, err
	}
	fmt.Printf("%s: created (owner %s) with sample contacts and deals\n", t.company, t.ownerEmail())
	return st, nil
}

// sampleData adds 12 contacts and 8 deals spread over every stage, so the
// dashboard and Kanban board have something to show.
func (s *seeder) sampleData(ctx context.Context, t seededTenant) error {
	names := []string{"Nguyễn Văn An", "Trần Thị Bình", "Lê Minh Chi", "Phạm Quốc Dũng", "Hoàng Gia Giang", "Huỳnh Ngọc Hà",
		"Phan Thanh Hải", "Vũ Thị Hoa", "Võ Đức Khoa", "Đặng Thu Lan", "Bùi Văn Long", "Đỗ Thị Mai"}
	ids := make([]uuid.UUID, len(names))
	for i, name := range names {
		email := fmt.Sprintf("khach%02d@%s", i+1, t.domain)
		phone := fmt.Sprintf("09%08d", i+1)
		c, err := s.contacts.Create(ctx, t.tenantID, t.ownerID, contact.CreateInput{FullName: name, Email: &email, Phone: &phone})
		if err != nil {
			return err
		}
		ids[i] = c.ID
	}

	type sample struct {
		title  string
		amount int64
		path   []deal.Stage // transitions applied after creation (deals start in lead)
	}
	samples := []sample{
		{"Website bán hàng", 45_000_000, nil},
		{"App đặt lịch", 120_000_000, nil},
		{"Bảo trì hệ thống", 18_000_000, []deal.Stage{deal.StageQualified}},
		{"Tư vấn chuyển đổi số", 60_000_000, []deal.Stage{deal.StageQualified}},
		{"CRM nội bộ", 250_000_000, []deal.Stage{deal.StageQualified, deal.StageProposal}},
		{"Thiết kế nhận diện", 30_000_000, []deal.Stage{deal.StageQualified, deal.StageProposal, deal.StageWon}},
		{"Hosting 1 năm", 12_000_000, []deal.Stage{deal.StageQualified, deal.StageProposal, deal.StageWon}},
		{"Hệ thống ERP", 900_000_000, []deal.Stage{deal.StageLost}},
	}
	for i, smp := range samples {
		d, err := s.deals.Create(ctx, t.tenantID, t.ownerID, deal.CreateInput{
			ContactID: ids[i%len(ids)], Title: smp.title, Amount: smp.amount,
		})
		if err != nil {
			return err
		}
		for _, to := range smp.path {
			in := deal.TransitionInput{To: to, Version: d.Version}
			if to == deal.StageLost {
				in.LostReason = "Ngân sách chưa phù hợp"
			}
			if d, err = s.deals.Transition(ctx, t.tenantID, t.ownerID, d.ID, in); err != nil {
				return err
			}
		}
	}
	return nil
}

// loadTestUsers creates member accounts so k6 can spread its requests over many
// users and stay under the 100 requests/minute/user limit (NFR-SEC-02).
func (s *seeder) loadTestUsers(ctx context.Context, t seededTenant, n int) error {
	hash, err := auth.HashPassword(s.password, auth.BcryptCost) // one hash for all: same password
	if err != nil {
		return err
	}
	created := 0
	for i := 1; i <= n; i++ {
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		u := auth.NewUser{ID: id, TenantID: t.tenantID, Email: fmt.Sprintf("loadtest-%03d@%s", i, t.domain),
			PasswordHash: hash, FullName: fmt.Sprintf("Load Test %03d", i), Role: auth.RoleMember}
		err = s.authRepo.WithTenant(ctx, t.tenantID, func(st auth.TenantStore) error { return st.CreateUser(ctx, u) })
		switch {
		case errors.Is(err, auth.ErrEmailTaken):
		case err != nil:
			return err
		default:
			created++
		}
	}
	fmt.Printf("%s: %d load-test users (loadtest-001..%03d@%s), %d new\n", t.company, n, n, t.domain, created)
	return nil
}

// bulkContacts inserts n contacts in one statement, as app_user (RLS applies).
func (s *seeder) bulkContacts(ctx context.Context, t seededTenant, n int) error {
	var inserted int64
	err := s.db.WithTenantTx(ctx, t.tenantID, func(tx pgx.Tx) error {
		var err error
		inserted, err = sqlc.New(tx).SeedContacts(ctx, sqlc.SeedContactsParams{
			TenantID: t.tenantID, OwnerID: t.ownerID, EmailDomain: t.domain,
			N: int32(n), //nolint:gosec // checked: at most 1,000,000
		})
		return err
	})
	if err != nil {
		return err
	}
	fmt.Printf("%s: %d load-test contacts requested, %d new\n", t.company, n, inserted)
	return nil
}
