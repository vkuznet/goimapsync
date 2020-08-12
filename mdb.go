package main

// Copyright (c) 2020 - Valentin Kuznetsov <vkuznet@gmail.com>
// DB module for goimapsync
//
// for Go database API: http://go-database-sql.org/overview.html
// tutorial: https://golang-basic.blogspot.com/2014/06/golang-database-step-by-step-guide-on.html
// SQLite driver:
//  _ "github.com/mattn/go-sqlite3"
//

import (
	"database/sql"
	"errors"
	"log"
	"os"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// InitDB sets pointer to mdb
func InitDB() (*sql.DB, error) {
	dbAttrs := strings.Split(Config.DBUri, "://")
	if len(dbAttrs) != 2 {
		return nil, errors.New("Please provide proper mdb uri")
	}
	dbDriver := dbAttrs[0]
	dbFileName := dbAttrs[1]
	newDB := false
	// create DB if it does not exists
	if _, err := os.Stat(dbFileName); os.IsNotExist(err) {
		createDB(dbFileName)
		newDB = true
	}
	db, err := sql.Open(dbDriver, dbFileName)
	if err != nil {
		return nil, err
	}
	err = db.Ping()
	db.SetMaxOpenConns(100)
	db.SetMaxIdleConns(100)
	if newDB {
		createTable(db)
	}
	return db, err
}

// helper function to create DB
func createDB(fname string) {
	log.Println("Creating", fname)
	file, err := os.Create(fname)
	if err != nil {
		log.Fatal(err.Error())
	}
	file.Close()
}

// helper function to create our table(s)
func createTable(db *sql.DB) {
	tableSQL := `CREATE TABLE messages (
		"id" INTEGER PRIMARY KEY AUTOINCREMENT,
		"timestamp" int NOT NULL,
		"hid" TEXT NOT NULL UNIQUE,
		"mid" TEXT NOT NULL UNIQUE,
		"path" TEXT NOT NULL,
		"imap" TEXT NOT NULL
	  );` // SQL Statement for Create Table

	statement, err := db.Prepare(tableSQL) // Prepare SQL Statement
	if err != nil {
		log.Fatal(err.Error())
	}
	statement.Exec() // Execute SQL Statements
	// create hid index
	tableSQL = `create unique index idx_hid on messages (hid);`
	statement, err = db.Prepare(tableSQL) // Prepare SQL Statement
	if err != nil {
		log.Fatal(err.Error())
	}
	statement.Exec() // Execute SQL Statements
}

// insertMessage inserts given message into DB
func insertMessage(m Message) error {
	tx, err := mdb.Begin()
	if err != nil {
		log.Printf("unable to start transaction in DB: %v\n", err)
		return err
	}
	defer tx.Rollback()
	var stmt string
	tstmp := time.Now().Unix()
	stmt = "INSERT INTO messages (timestamp, hid, mid, path, imap) VALUES (?,?,?,?,?)"
	_, err = tx.Exec(stmt, tstmp, m.HashId, m.MessageId, m.Path, m.Imap)
	if err != nil {
		log.Printf("unable to execute statement '%s' in DB: %v\n", stmt, err)
		return tx.Rollback()
	}
	err = tx.Commit()
	if err != nil {
		log.Printf("unable to commit transaction in DB: %v\n", err)
		return tx.Rollback()
	}
	return nil
}

// deleteMessage deletes given message in DB
func deleteMessage(hid string) error {
	tx, err := mdb.Begin()
	if err != nil {
		log.Printf("unable to start transaction in DB: %v\n", err)
		return err
	}
	defer tx.Rollback()
	var stmt string
	stmt = "DELETE FROM messages WHERE hid=?"
	_, err = tx.Exec(stmt, hid)
	if err != nil {
		log.Printf("unable to execute statement '%s' in DB: %v\n", stmt, err)
		return tx.Rollback()
	}
	err = tx.Commit()
	if err != nil {
		log.Printf("unable to commit transaction in DB: %v\n", err)
		return tx.Rollback()
	}
	return nil
}

// helper function to find message in DB
func findMessage(hid string) (Message, error) {
	var m Message
	// proceed with transaction operation
	tx, err := mdb.Begin()
	if err != nil {
		log.Printf("unable to start transaction in DB: %v\n", err)
		return m, err
	}
	defer tx.Rollback()
	// look-up files info
	stmt := "SELECT hid, mid, path, imap FROM messages WHERE hid=?"
	res, err := tx.Query(stmt, hid)
	if err != nil {
		log.Printf("unable to query DB: %v\n", err)
		return m, err
	}
	for res.Next() {
		var hid, mid, path, imap string
		err = res.Scan(&hid, &mid, &path, &imap)
		if err != nil {
			log.Printf("unable to scan in DB: %v\n", err)
			return m, tx.Rollback()
		}
		m = Message{HashId: hid, MessageId: mid, Path: path, Imap: imap}
		return m, nil
	}
	return m, nil
}

// helper function to get all messages from local DB
func getDBMessages() ([]Message, error) {
	var mlist []Message
	// proceed with transaction operation
	tx, err := mdb.Begin()
	if err != nil {
		log.Printf("unable to start transaction in DB: %v\n", err)
		return mlist, err
	}
	defer tx.Rollback()
	// look-up files info
	stmt := "SELECT hid, mid, path, imap FROM messages"
	res, err := tx.Query(stmt)
	if err != nil {
		log.Printf("unable to query DB: %v\n", err)
		return mlist, err
	}
	for res.Next() {
		var hid, mid, path, imap string
		err = res.Scan(&hid, &mid, &path, &imap)
		if err != nil {
			log.Printf("unable to scan in DB: %v\n", err)
			return mlist, tx.Rollback()
		}
		m := Message{HashId: hid, MessageId: mid, Path: path, Imap: imap}
		mlist = append(mlist, m)
	}
	return mlist, nil
}
