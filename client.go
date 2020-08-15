package main

// Copyright (c) 2020 - Valentin Kuznetsov <vkuznet AT gmail dot com>
// goimapsync main module, it implements the following actions:
// - "sync" mails from local maildir to IMAP
// - "fetch-new" new mails from IMAP to local maildir folder
// - "fetch-all" all mails from IMAP to local maildir folder
// - "move" mail(s) on IMAP server to given folder and message id

import (
	"bufio"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/mail"
	"os"
	"strings"
	"sync"
	"time"

	imap "github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

// global variable to keep pointer to mdb
var mdb *sql.DB

// global variables we use across the code
var imapFolders map[string][]string
var hostname string

// Message structure holds all information about emails message
type Message struct {
	Path      string   // location of the message in local file dir
	MessageId string   // message Id
	Flags     []string // message Flags
	Imap      string   // name of imap server it belongs to
	Subject   string   // message subject
	SeqNumber uint32   // message sequence number
	HashId    string   // message id md5 hash
}

// String function dumps Message info
func (m *Message) String() string {
	return fmt.Sprintf("<Imap:%s SeqNum:%v HashId:%v Path:%s MessageId:%s Flags:%v Subject: %s>", m.Imap, m.SeqNumber, m.HashId, m.Path, m.MessageId, m.Flags, m.Subject)
}

// ServerClient structure which holds IMAP server alias name and connection pointer
type ServerClient struct {
	Name   string         // name of IMAP server
	Client *client.Client // connected client to IMAP server
}

// helper function which extracts flags from given email file name
func getFlags(fname string) []string {
	// example of file name in our Inbox
	// <tstamp.id.hostname:2,flags>
	if strings.Contains(fname, ",") {
		arr := strings.Split(fname, ",")
		flags := arr[len(arr)-1]
		return strings.Split(flags, "")
	}
	return []string{}
}

// helper function which extracts message id from given email file
func getMessageId(fname string) string {
	file, err := os.Open(fname)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(strings.ToLower(scanner.Text()), "message-id:") {
			arr := strings.Split(line, ":")
			if len(arr) > 0 {
				return strings.Trim(arr[1], " ")
			}
		}
	}
	return ""
}

// helper function to connect to our IMAP servers
func connect() map[string]*client.Client {
	defer timing("connect", time.Now())
	defer profiler("connect")()
	cmap := make(map[string]*client.Client)

	ch := make(chan ServerClient, len(Config.Servers))
	defer close(ch)
	for _, srv := range Config.Servers {
		go func(s Server) {
			var c *client.Client
			var err error
			if s.UseTls {
				c, err = client.DialTLS(s.Uri, nil)
			} else {
				c, err = client.Dial(s.Uri)
			}
			if err != nil {
				log.Fatal(err)
			}
			if err := c.Login(s.Username, s.Password); err != nil {
				log.Fatal(err)
			}
			if Config.Verbose > 0 {
				log.Println("Logged into", s.Uri)
			}
			ch <- ServerClient{Name: s.Name, Client: c}
		}(srv)
	}
	for i := 0; i < len(Config.Servers); i++ {
		s := <-ch
		cmap[s.Name] = s.Client
	}
	return cmap
}

// helper function to logout from all IMAP clients
func logout(cmap map[string]*client.Client) {
	for _, c := range cmap {
		c.Logout()
	}
}

// helper function which takes a snapshot of remote IMAP servers
// and return list of messages
func readImap(c *client.Client, imapName, folder string, newMessages bool) []Message {
	defer timing("readImap", time.Now())
	defer profiler("readImap")()

	// Select given imap folder
	mbox, err := c.Select(folder, false)
	if err != nil {
		log.Printf("Folder '%s' on '%s', error: %v\n", folder, imapName, err)
		return []Message{}
	}

	// get messages
	seqset := new(imap.SeqSet)
	var nmsg uint32
	if newMessages {
		// get only new messages
		criteria := imap.NewSearchCriteria()
		criteria.WithoutFlags = []string{imap.SeenFlag}
		ids, err := c.Search(criteria)
		if err != nil {
			log.Fatal(err)
		}
		if len(ids) > 0 {
			seqset.AddNum(ids...)
			nmsg = uint32(len(ids))
			log.Printf("Found %d new message(s) in folder '%s' on '%s'\n", nmsg, folder, imapName)
		} else {
			log.Printf("No new messages in folder '%s' on '%s'\n", folder, imapName)
			return []Message{}
		}
	} else {
		// get all messages from mailbox
		from := uint32(1)
		to := mbox.Messages
		seqset.AddRange(from, to)
		nmsg = to
	}
	// section will be used only in writeContent
	section := &imap.BodySectionName{}

	messages := make(chan *imap.Message, nmsg)
	items := []imap.FetchItem{section.FetchItem(), imap.FetchFlags, imap.FetchEnvelope}
	// TODO: use goroutine until this issue will be solved
	// https://github.com/emersion/go-imap/issues/382
	done := make(chan error, 1)
	go func() {
		done <- c.Fetch(seqset, items, messages)
	}()
	//     err = c.Fetch(seqset, items, messages)
	//     if err != nil {
	//         log.Fatal(err)
	//     }

	seqNum := uint32(1)
	var msgs []Message
	var wg sync.WaitGroup
	for msg := range messages {
		var m Message
		if msg == nil || msg.Envelope == nil {
			continue
		}
		mid := msg.Envelope.MessageId
		sub := msg.Envelope.Subject
		hid := md5hash(mid)
		flags := msg.Flags
		m = Message{MessageId: mid, Flags: flags, Imap: imapName, Subject: sub, SeqNumber: seqNum, HashId: hid}
		if mid == "" || hid == "" {
			log.Printf("read empty mail %s %v out of %v from %s\n", m.String(), seqNum, nmsg, imapName)
			continue
		}
		log.Printf("read %s %v out of %v from %s\n", m.String(), seqNum, nmsg, imapName)
		r := msg.GetBody(section)
		entry, e := findMessage(hid)
		if Config.Verbose > 1 {
			log.Println("hid", hid, "DB entry", entry.String(), e)
		}
		if e == nil && entry.HashId == hid {
			if Config.Verbose > 0 {
				log.Println("Mail with hash", hid, "already exists")
			}
		} else {
			if newMessages {
				m.Flags = append(m.Flags, imap.RecentFlag)
			}
			if !isMailWritten(m) {
				wg.Add(1)
				go writeMail(imapName, folder, m, r, &wg)
			} else {
				// check if mail is presented in our DB, if not we should insert its entry
				msg, e := findMessage(m.HashId)
				if e == nil && msg.HashId == "" {
					m.Path = findPath(m.Imap, m.HashId)
					insertMessage(m)
				}
			}
		}
		msgs = append(msgs, m)
		seqNum += 1
	}
	wg.Wait()
	return msgs
}

// helper function to find message path in local maildir
func findPath(imapName, hid string) string {
	var mdict map[string]string
	if Config.CommonInbox {
		mdict = readMaildir("", "INBOX")
	} else {
		for k, v := range readMaildir(imapName, "INBOX") {
			mdict[k] = v
		}
	}
	if path, ok := mdict[hid]; ok {
		return path
	}
	return ""
}

// helper function to check if mail was previously written in local maildir
func isMailWritten(m Message) bool {
	var mdict map[string]string
	if Config.CommonInbox {
		mdict = readMaildir("", "INBOX")
	} else {
		for k, v := range readMaildir(m.Imap, "INBOX") {
			mdict[k] = v
		}
	}
	for hid := range mdict {
		if hid == m.HashId {
			return true
		}
	}
	return false
}

// helper function to create an md5 hash of given message Id
func md5hash(mid string) string {
	h := md5.New()
	h.Write([]byte(mid))
	return hex.EncodeToString(h.Sum(nil))
}

// helper function to create maildir map of existing mails
func readMaildir(imapName, folder string) map[string]string {

	// when writing to local mail dir the folder name should not cotain slashes
	// replace slash with dot
	folder = strings.Replace(folder, "/", ".", -1)
	// create proper dir structure in maildir area
	var dirs = []string{"cur", "new", "tmp"}
	fdir := localFolder(imapName, folder)
	for _, d := range dirs {
		fpath := fmt.Sprintf("%s/%s/%s", Config.Maildir, fdir, d)
		os.MkdirAll(fpath, os.ModePerm)
	}
	if Config.Verbose > 0 {
		log.Println("Read local mails from", fmt.Sprintf("%s/%s", Config.Maildir, fdir))
	}

	// create mail dict which we'll return upstream
	mdict := make(map[string]string)
	// each file in maildir has format: <tstamp.hid.hostname:2,flags>
	for _, d := range dirs {
		root := fmt.Sprintf("%s/%s/%s", Config.Maildir, fdir, d)
		files, err := ioutil.ReadDir(root)
		if err != nil {
			log.Println("Error reading", root, err)
			continue
		}
		for _, f := range files {
			fname := fmt.Sprintf("%s/%s", root, f.Name())
			arr := strings.Split(fname, ".")
			mdict[arr[1]] = fname
		}
	}
	return mdict
}

// helper to get local maildir folder
func localFolder(imapName, folder string) string {
	if imapName == "" {
		return folder
	} else if strings.ToLower(folder) == "inbox" && Config.CommonInbox {
		return folder
	}
	return fmt.Sprintf("%s/%s", imapName, folder)
}

// helper function to return short flag names
func flagSymbols(flags []string) string {
	var flag string
	for _, f := range flags {
		f = strings.ToLower(strings.Replace(f, "\\", "", -1))
		if f == "seen" {
			f = "S"
		} else if f == "recent" {
			f = "N"
		} else if f == "answered" {
			f = "A"
		} else if f == "junk" {
			f = "J"
		} else {
			continue
		}
		flag = fmt.Sprintf("%s%s", flag, f)
	}
	if flag == "" {
		flag = "S"
	}
	return flag
}

// helper function to write emails in imapName folder of local maildir
func writeMail(imapName, folder string, m Message, r io.Reader, wg *sync.WaitGroup) {
	hid := m.HashId  // hash id of the message id
	flags := m.Flags // message flags
	defer timing("writeMail", time.Now())
	defer profiler("writeMail")()
	defer wg.Done()

	// construct file name with the following format:
	// tstamp.hid.hostname:2,flags
	tstamp := time.Now().Unix()
	flag := flagSymbols(flags)
	if Config.Verbose > 0 {
		log.Println("writeMail", tstamp, hid, flags, flag)
	}
	fdir := localFolder(imapName, folder)
	fname := fmt.Sprintf("%d.%s.%s:2,%s", tstamp, hid, hostname, flag)
	fpath := fmt.Sprintf("%s/%s/cur/%s", Config.Maildir, fdir, fname)
	if strings.Contains(flag, "N") {
		fname = fmt.Sprintf("%d.%s.%s", tstamp, hid, hostname)
		fpath = fmt.Sprintf("%s/%s/new/%s", Config.Maildir, fdir, fname)
	}
	// check if our file exist
	if _, err := os.Stat(fpath); err == nil {
		if Config.Verbose > 0 {
			log.Println("File", fpath, "already exists")
		}
		return
	}
	// proceed and create a file with our email
	file, err := os.Create(fpath)
	if err != nil {
		log.Println("unable to open", fpath, err)
		return
	}
	defer file.Close()
	msg, err := mail.ReadMessage(r)
	if err != nil {
		log.Println("Unable to read a message", err)
		return
	}
	// write headers
	for k, v := range msg.Header {
		line := fmt.Sprintf("%s: %s\n", k, strings.Join(v, " "))
		_, e := file.WriteString(line)
		if e != nil {
			log.Printf("unable to write '%s', error: %v\n", line, e)
			return
		}
	}
	// write body
	body, err := ioutil.ReadAll(msg.Body)
	if err == nil {
		_, e := file.Write(body)
		if e != nil {
			log.Println("unable to write msg body, error", e)
			return
		}
	}
	// write message info into DB
	m.Path = fpath
	err = insertMessage(m)
	if err != nil {
		log.Fatal("message was written to file-system but not in DB, error: ", err)
	}
}

// helper function to get list of all imap folders
func getImapFolders(c *client.Client, imapName string) []string {
	// List mailboxes
	mailboxes := make(chan *imap.MailboxInfo, 10)
	done := make(chan error, 1)
	go func() {
		done <- c.List("", "*", mailboxes)
	}()

	var folders []string
	for m := range mailboxes {
		folders = append(folders, m.Name)
	}

	if err := <-done; err != nil {
		log.Fatal(err)
	}
	return folders
}

// helper function to get folder name for given IMAP server
func imapFolder(imapName, folder string) string {
	// if no folder is given, we'll immediately return
	if folder == "" {
		return folder
	}
	folders := imapFolders[imapName]
	for _, f := range folders {
		if strings.ToLower(f) == strings.ToLower(folder) {
			return f
		}
	}
	// defaults
	if strings.ToLower(folder) == "inbox" {
		return "INBOX"
	}
	if strings.ToLower(folder) == "spam" {
		return "Spam"
	}
	// at this point we should through an error
	log.Fatalf("No folder '%s' found in imap '%s' folder list '%v'\n", folder, imapName, folders)
	return ""
}

// MoveMessage moves message in given imap server into specifc folder
func MoveMessage(c *client.Client, imapName string, msg Message, folderName string) {
	defer timing("MoveMessage", time.Now())
	defer profiler("MoveMessage")()
	// inbox folder
	inboxFolder := imapFolder(imapName, "inbox")
	folder := imapFolder(imapName, folderName)

	seqset := new(imap.SeqSet)
	seqset.AddNum(msg.SeqNumber)

	// connect to given Spam folder
	_, err := c.Select(inboxFolder, false)
	if err != nil {
		log.Fatal(err)
	}

	if folder == "" {
		log.Printf("delete %v\n", msg.String())
	} else {
		log.Printf("move %v to '%s' on %s\n", msg.MessageId, folder, imapName)
	}

	// copy mail to folder
	if folder != "" {
		// mark mail as seen in our inbox
		item := imap.FormatFlagsOp(imap.AddFlags, true)
		flags := []interface{}{imap.SeenFlag}
		if err := c.Store(seqset, item, flags, nil); err != nil {
			log.Fatal(err)
		}
		if err := c.Copy(seqset, folder); err != nil {
			log.Fatal(err)
		}
	}
	// mark mail as deleted on IMAP server
	item := imap.FormatFlagsOp(imap.AddFlags, true)
	flags := []interface{}{imap.DeletedFlag}
	if err := c.Store(seqset, item, flags, nil); err != nil {
		log.Fatal(err)
	}
	// then delete it in inbox folder
	if err := c.Expunge(nil); err != nil {
		log.Fatal(err)
	}
}

// Move message on IMAP to a given folder, if folder name is not given the mail
// will be deleted
func Move(c *client.Client, imapName, match, folderName string) {
	if folderName == "" || match == "" {
		log.Fatal("Move operation requires both folder and message id")
	}
	defer timing("Move", time.Now())
	defer profiler("Move")()
	// inbox folder
	inboxFolder := imapFolder(imapName, "inbox")
	folder := imapFolder(imapName, folderName)

	// check if given match is existing file, if so we'll
	// extract from it MatchedId
	if _, err := os.Stat(match); err == nil {
		match = getMessageId(match)
	}

	// Select INBOX
	mbox, err := c.Select(inboxFolder, false)
	if err != nil {
		log.Fatal(err)
	}

	// Get messages from INBOX
	from := uint32(1)
	to := mbox.Messages
	seqset := new(imap.SeqSet)
	seqset.AddRange(from, to)
	if Config.Verbose > 0 {
		log.Println("Fetch from IMAP", imapName, folderName, from, to)
	}

	messages := make(chan *imap.Message, to)
	items := []imap.FetchItem{imap.FetchFlags, imap.FetchUid, imap.FetchEnvelope}
	// TODO: use goroutine until this issue will be solved
	// https://github.com/emersion/go-imap/issues/382
	done := make(chan error, 1)
	go func() {
		done <- c.Fetch(seqset, items, messages)
	}()
	//     err = c.Fetch(seqset, items, messages)
	//     if err != nil {
	//         log.Fatal(err)
	//     }

	seqNum := uint32(1)
	for msg := range messages {
		if Config.Verbose > 1 {
			log.Println("* "+msg.Envelope.Subject+" MessageId ", msg.Envelope.MessageId)
		}
		if msg.Envelope.MessageId == match {
			if Config.Verbose > 0 {
				log.Printf("Found match: seq:%v Envelope: %+v Flags: %+v\n", seqNum, msg.Envelope, msg.Flags)
			}
			mid := msg.Envelope.MessageId
			hid := md5hash(mid)
			sub := msg.Envelope.Subject
			flags := msg.Flags
			m := Message{HashId: hid, MessageId: mid, Flags: flags, Imap: imapName, Subject: sub, SeqNumber: seqNum}
			MoveMessage(c, imapName, m, folder)
			return
		}
		seqNum += 1
	}
}

// Fetch content of given folder from IMAP into local maildir
func Fetch(c *client.Client, imapName, folder string, newMessages bool) {
	defer timing("Fetch", time.Now())
	defer profiler("Fetch")()
	log.Printf("Fetch %s from %s\n", folder, imapName)
	for _, m := range readImap(c, imapName, folder, newMessages) {
		if Config.Verbose > 0 {
			log.Println("fetch", m.String())
		}
	}
}

// Sync provides sync between local maildir and IMAP servers
func Sync(cmap map[string]*client.Client, dryRun bool) {
	defer timing("Sync", time.Now())
	defer profiler("Sync")()

	var mlist []Message
	for imapName, c := range cmap {
		// read new messages from IMAP
		newMessages := true
		log.Println("### read new messages on", imapName)
		for _, m := range readImap(c, imapName, "INBOX", newMessages) {
			if Config.Verbose > 0 {
				log.Println("Read new message", m.String())
			}
		}
		// read all messages from IMAP, since we may miss some of them
		// in local maildir if those were read on another device(s)
		// this step will ensure that we get local copies of non-new messages
		log.Println("### read all messages on", imapName)
		newMessages = false
		for _, msg := range readImap(c, imapName, "INBOX", newMessages) {
			mlist = append(mlist, msg)
		}
	}

	// get local maildir snapshot
	log.Println("### read local maildir")
	var mdict map[string]string
	if Config.CommonInbox {
		mdict = readMaildir("", "INBOX")
	} else {
		for name := range cmap {
			for k, v := range readMaildir(name, "INBOX") {
				mdict[k] = v
			}
		}
	}

	// now loop over messages we got from IMAP and compare with our local maildir
	// then we collect message ids for deletion
	var dlist []Message
	for _, msg := range mlist {
		// check if our message exists in DB
		m, e := findMessage(msg.HashId)
		if e == nil && m.HashId == msg.HashId {
			// we found message in DB, check if it exists in local maildir
			if _, ok := mdict[m.HashId]; !ok {
				// message is not found in local maildir and we need to delete it
				if dryRun {
					log.Println("dry-run expunge", msg.String())
				} else {
					dlist = append(dlist, msg)
				}
			}
		}
	}
	if !dryRun {
		removeImapMessages(cmap, dlist)
	}
}

// helper function to remove messages in IMAP server(s)
// it takes list of messages
func removeImapMessages(cmap map[string]*client.Client, mlist []Message) {
	defer timing("removeImapMessages", time.Now())
	defer profiler("removeImapMessages")()

	if Config.Verbose > 0 {
		log.Println("removeImapMessages", mlist)
	}
	for imapName, c := range cmap {
		// select messages from IMAP inbox folder
		inboxFolder := imapFolder(imapName, "inbox")
		_, err := c.Select(inboxFolder, false)
		if err != nil {
			log.Fatal(err)
		}

		// get list of message seq numbers for our IMAP server
		var slist []uint32
		var hlist []string
		for _, m := range mlist {
			if m.Imap == imapName {
				slist = append(slist, m.SeqNumber)
				hlist = append(hlist, m.HashId)
			}
		}
		seqset := new(imap.SeqSet)
		seqset.AddNum(slist...)
		if Config.Verbose > 0 {
			log.Printf("%s, remove seqset: %v\n", imapName, seqset)
		}
		if seqset.Empty() {
			continue
		}
		// now we mark messages for deletion in IMAP
		item := imap.FormatFlagsOp(imap.AddFlags, true)
		flags := []interface{}{imap.DeletedFlag}
		if err := c.Store(seqset, item, flags, nil); err != nil {
			log.Fatal(err)
		}
		// delete messages on IMAP server
		if err := c.Expunge(nil); err != nil {
			log.Fatal(err)
		}
		// delete messages in local maildir DB
		for _, hid := range hlist {
			deleteMessage(hid)
		}
	}
}

// helper function to report timing of given function
func timing(name string, start time.Time) {
	if Config.Verbose > 0 {
		log.Printf("Function '%s' elapsed time: %v\n", name, time.Since(start))
	} else if name == "main" {
		log.Printf("elapsed time: %v\n", time.Since(start))
	}
}

func main() {
	var config string
	flag.StringVar(&config, "config", os.Getenv("HOME")+"/.goimapsyncrc", "config JSON file")
	var dryRun bool
	flag.BoolVar(&dryRun, "dryRun", false, "perform dry-run")
	var mid string
	flag.StringVar(&mid, "mid", "", "mail file or messageid to use")
	var folder string
	flag.StringVar(&folder, "folder", "INBOX", "folder to use")
	var op string
	flag.StringVar(&op, "op", "sync", "perform given operation")
	var profiler string
	flag.StringVar(&profiler, "profiler", "", "profiler file name")
	var verbose int
	flag.IntVar(&verbose, "verbose", 0, "verbosity level")
	flag.Usage = func() {
		fmt.Println("Usage: goimapsync [options]")
		flag.PrintDefaults()
		fmt.Println("Supported operations:")
		fmt.Println("   fetch-new: to get list of new messages from specified IMAP folder")
		fmt.Println("   fetch-all: to get list of all messages from specified IMAP folder")
		fmt.Println("   move     : to move givem message on IMAP server, e.g. send to Spam")
		fmt.Println("   sync     : to sync local maildir with IMAP server(s)")
		fmt.Println("Examples:")
		fmt.Println("   # fetch new messages from given IMAP folder")
		fmt.Println("   goimapsync -config config.json -op=fetch-new -folder=MyFolder")
		fmt.Println("   # fetch all messages from given IMAP folder")
		fmt.Println("   goimapsync -config config.json -op=fetch-all -folder=MyFolder")
		fmt.Println("   # sync mails form local maildir to IMAP")
		fmt.Println("   goimapsync -config config.json -op=sync")
		fmt.Println("   # the same operation with encrypted (gpg) config")
		fmt.Println("   gpg -d -o - $HOME/.goimapsync.gpg | goimapsync -op=sync -config -")
		fmt.Println("   # sync mails form given IMAP folder into local maildir")
		fmt.Println("   gpg -d -o - $HOME/.goimapsync.gpg | goimapsync -config config.json -op=fetch -folder=MyFolder")
		fmt.Println("   # move given mail id in IMAP server to given folder")
		fmt.Println("   goimapsync -config config.json -op=move -mid=123 -folder=MyFolder")
	}
	flag.Parse()
	ParseConfig(config)
	log.SetFlags(log.LstdFlags)
	// overwrite verbose level in config
	if verbose > 0 {
		Config.Verbose = verbose
		log.SetFlags(log.LstdFlags | log.Lshortfile)
	}
	if profiler != "" {
		Config.Profiler = profiler
		initProfiler(profiler)
	}
	// add timing profile
	defer timing("main", time.Now())

	// init imap folders map
	var err error
	imapFolders = make(map[string][]string)
	hostname, err = os.Hostname()
	if err != nil {
		log.Fatal(err)
	}

	// init our message db
	mdb, err = InitDB()
	if err != nil {
		log.Fatal(err)
	}

	// connect to our IMAP servers
	cmap := connect()
	defer logout(cmap)

	for imapName, c := range cmap {
		folders := getImapFolders(c, imapName)
		imapFolders[imapName] = folders
		if Config.Verbose > 0 {
			log.Println("IMAP", imapName, folders)
		}
	}
	switch op {
	case "move":
		// perform move action for given message id and IMAP folder
		for name, c := range cmap {
			Move(c, name, mid, folder)
		}
	case "fetch-new":
		// fetch new messages for given IMAP folder
		for name, c := range cmap {
			Fetch(c, name, folder, true)
		}
	case "fetch-all":
		// fetch all messages (old and new) for given IMAP folder
		for name, c := range cmap {
			Fetch(c, name, folder, false)
		}
	case "sync":
		// sync emails between local maildir and IMAP server
		Sync(cmap, dryRun)
	default:
		log.Fatalf("Given operation '%s' is not supported, please use sync, fetch-new, fetch-all\n", op)
	}
}
