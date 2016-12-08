package timedb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/BurntSushi/migration"
	"github.com/IMQS/log"
	_ "github.com/lib/pq"
	"io/ioutil"
	"strings"
	"time"
)

type Config struct {
	Driver   string
	Host     string
	Database string
	Username string
	Password string
}

type TimeDB struct {
	Log    *log.Logger
	Config Config
	Conn   *sql.DB
}

type caches struct {
	titleToTicket map[string]int64
	emailToUser   map[string]int64
}

func newCaches() *caches {
	c := &caches{}
	c.titleToTicket = map[string]int64{}
	c.emailToUser = map[string]int64{}
	return c
}

// These don't strictly belong in here, but where else?
const (
	SystemTypeAnon    = "anon"
	SystemTypeJira    = "jira"
	SystemTypeTMetric = "tmet"
)

const (
	TicketTypeBug       = "bug"
	TicketTypeFeature   = "feat"
	TicketTypeBAU       = "bau" // business as usual
	TicketTypeTest      = "test"
	TicketTypeInterrupt = "intr"
	TicketTypeEpic      = "epic"
	TicketTypeSpike     = "spike"
	TicketTypeOther     = "other"
	TicketTypeAnon      = "anon"
)

// This format was built to work with TMetric output
// Here there is no systemid, so we synthesize it, by assuming that each time entry
// covers exactly one day. Thus, the systemid is combination of the ticketid and the day
type TimeFormat1 struct {
	System    string
	Email     string
	TaskTitle string
	Start     time.Time
	End       time.Time
}

// This format was built to work with JIRA output
type IssueFormat1 struct {
	System      string
	SystemID    string
	Title       string
	Type        string
	StoryPoints int
	CreateTime  time.Time
}

func isKeyViolation(err error) bool {
	return strings.Index(err.Error(), "duplicate key value violates unique constraint") != -1
}

func (t *TimeDB) LoadConfig() error {
	filename := "config/timedb.json"
	bytes, err := ioutil.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("Error loading config file %v: %v", filename, err)
	}
	if err := json.Unmarshal(bytes, &t.Config); err != nil {
		return fmt.Errorf("Error decoding config file %v: %v", filename, err)
	}
	return nil
}

func (t *TimeDB) Connect() error {
	migrations := []string{
		`
		-- userid is only applicable to anonymous tickets
		CREATE TABLE tickets (ticketid BIGSERIAL PRIMARY KEY, system VARCHAR, systemid VARCHAR, title VARCHAR, ticket_type VARCHAR, story_points INTEGER, userid BIGINT, create_time TIMESTAMP);
		CREATE UNIQUE INDEX idx_tickets_systemid ON tickets (system, systemid);
		CREATE INDEX idx_tickets_title ON tickets (title);

		CREATE TABLE users (userid BIGSERIAL PRIMARY KEY, email VARCHAR);
		CREATE UNIQUE INDEX idx_users_email ON users (lower(email));

		CREATE TABLE times (userid BIGINT, system VARCHAR, systemid VARCHAR, start_time TIMESTAMP, end_time TIMESTAMP, ticketid BIGINT);
		CREATE INDEX idx_times_userid ON times (userid);
		CREATE INDEX idx_times_ticket ON times (ticketid);
		CREATE UNIQUE INDEX idx_times_systemid ON times (system, systemid);
		`,
	}

	migs := []migration.Migrator{}
	for _, m := range migrations {
		copy := m
		migs = append(migs, func(tx migration.LimitedTx) error {
			_, err := tx.Exec(copy)
			return err
		})
	}

	var err error
	cstr := fmt.Sprintf("user=%v password=%v dbname=%v sslmode=disable", t.Config.Username, t.Config.Password, t.Config.Database)
	if t.Conn, err = migration.Open(t.Config.Driver, cstr, migs); err != nil {
		return err
	}

	/*
		email := "Hello"
		_, err = t.Conn.Exec("INSERT INTO users (email) VALUES (lower($1))", email)
		_, err = t.Conn.Exec("INSERT INTO users (email) VALUES (lower($1))", email)
		userid := int64(0)
		//err = t.Conn.QueryRow("SELECT userid FROM users WHERE lower(email) = lower($1)", email).Scan(&userid)
		if err != nil {
			if err == sql.ErrNoRows {
				fmt.Printf("ErrNoRows\n")
			} else if isKeyViolation(err) {
				fmt.Printf("key violation\n")
			} else {
				fmt.Printf("query error : %v", err)
			}
		} else {
			fmt.Printf("no query error. id = %v\n", userid)
		}
	*/

	return nil
}

func (t *TimeDB) InsertIssues1(issues []IssueFormat1) error {
	tx, err := t.Conn.Begin()
	if err != nil {
		return err
	}

	for _, issue := range issues {
		var res sql.Result
		if res, err = tx.Exec("UPDATE tickets SET title = $1, ticket_type = $2, story_points = $3 WHERE system = $4 AND systemid = $5",
			issue.Title, issue.Type, issue.StoryPoints, issue.System, issue.SystemID); err != nil {
			break
		}
		if affected, _ := res.RowsAffected(); affected == 0 {
			if _, err = tx.Exec("INSERT INTO tickets (system, systemid, title, ticket_type, story_points, create_time) VALUES ($1, $2, $3, $4, $5, $6)",
				issue.System, issue.SystemID, issue.Title, issue.Type, issue.StoryPoints, issue.CreateTime); err != nil {
				break
			}
		}
		//ticketid := int64(0)
		//if err = tx.QueryRow("SELECT ticketid FROM tickets WHERE system = $1 AND systemid = $2", issue.System, issue.SystemID).Scan(&ticketid); err != nil {
		//	break
		//}
	}

	if err != nil {
		tx.Rollback()
		return err
	} else {
		return tx.Commit()
	}
}

func (t *TimeDB) InsertTimes1(times []TimeFormat1) error {
	cache := newCaches()
	tx, err := t.Conn.Begin()
	if err != nil {
		return err
	}
	for _, tt := range times {
		userid := int64(0)
		if userid, err = t.emailToUser(tx, cache, tt.Email); err != nil {
			break
		}
		ticketid := int64(0)
		if ticketid, err = t.titleToTicket(tx, cache, userid, tt.TaskTitle, true); err != nil {
			break
		}
		systemid := t.generateTimeSystemIDForDay(ticketid, tt.Start)
		var resp sql.Result
		resp, err = tx.Exec("UPDATE times SET start_time = $1, end_time = $2 WHERE system = $3 AND systemid = $4", tt.Start, tt.End, tt.System, systemid)
		if err != nil {
			break
		}
		rows_affected := int64(0)
		rows_affected, err = resp.RowsAffected()
		if err != nil {
			return err
		}
		if rows_affected == 0 {
			_, err = tx.Exec("INSERT INTO times (userid, system, systemid, start_time, end_time, ticketid) VALUES ($1, $2, $3, $4, $5, $6)", userid, tt.System, systemid, tt.Start, tt.End, ticketid)
			if err != nil {
				break
			}
		}
	}

	if err != nil {
		tx.Rollback()
		return err
	} else {
		return tx.Commit()
	}
}

func generateAnonTaskName(userid int64, title string) string {
	return fmt.Sprintf("anon(%v): %v", userid, title)
}

// Returns the ticketid for the new task
func (t *TimeDB) createAnonymousTask(tx *sql.Tx, userid int64, title string) (int64, error) {
	anonTitle := generateAnonTaskName(userid, title)
	_, err := tx.Exec("INSERT INTO tickets (system, title, ticket_type, userid) VALUES ($1, $2, $3, $4)", SystemTypeAnon, anonTitle, TicketTypeAnon, userid)
	if err != nil {
		return 0, err
	}
	ticketid := int64(0)
	if err = tx.QueryRow("SELECT ticketid FROM tickets WHERE system = $1 AND title = $2 AND userid = $3", SystemTypeAnon, anonTitle, userid).Scan(&ticketid); err != nil {
		return 0, err
	}
	return ticketid, nil
}

func (t *TimeDB) titleToTicket(tx *sql.Tx, cache *caches, userid int64, title string, createAnon bool) (int64, error) {
	ticket, err := t.titleToTicketRaw(tx, cache, title)
	if ticket != 0 || err != nil {
		return ticket, err
	}
	ticket, err = t.titleToTicketRaw(tx, cache, generateAnonTaskName(userid, title))
	if ticket != 0 || err != nil {
		return ticket, err
	}

	if !createAnon {
		return 0, nil
	}

	t.Log.Infof("Unable to find ticket '%v' for userid = %v. Creating an anonymous task", title, userid)
	return t.createAnonymousTask(tx, userid, title)
}

// Returns 0, nil  if no such ticket found
// Returns !0, nil if ticket found
// Return 0, err   if an error occurred
func (t *TimeDB) titleToTicketRaw(tx *sql.Tx, cache *caches, title string) (int64, error) {
	if id, ok := cache.titleToTicket[title]; ok {
		return id, nil
	}
	ticketid := int64(0)
	err := tx.QueryRow("SELECT ticketid FROM tickets WHERE title = $1 ORDER BY create_time DESC LIMIT 1", title).Scan(&ticketid)
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}
	cache.titleToTicket[title] = ticketid
	return ticketid, nil
}

// Same return values as titleToTicket
func (t *TimeDB) emailToUser(tx *sql.Tx, cache *caches, email string) (int64, error) {
	if id, ok := cache.emailToUser[email]; ok {
		return id, nil
	}
try_again:
	userid := int64(0)
	err := tx.QueryRow("SELECT userid FROM users WHERE lower(email) = lower($1)", email).Scan(&userid)
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}
	if err == sql.ErrNoRows {
		// insert user
		_, err = tx.Exec("INSERT INTO users (email) VALUES (lower($1))", email)
		if err != nil {
			return 0, err
		}
		goto try_again
	}
	cache.emailToUser[email] = userid
	return userid, nil
}

// Generate a fake times systemid value, assuming that the system generates a summary report, where
// each task is listed just one, so we'll only ever have a single entry per day, for any ticket.
// This will break, and produce extra hours, if a user goes back and rewrites history to such a
// degree that a task that once was worked on on a day, is now no longer visible on that day at all.
func (t *TimeDB) generateTimeSystemIDForDay(ticketid int64, start time.Time) string {
	return fmt.Sprintf("%v:%v", ticketid, start.Year()+start.YearDay())
}
