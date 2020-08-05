package main

// Copyright (c) 2020 - Valentin Kuznetsov <vkuznet AT gmail dot com>
// goimapsync module implements the following actions:
// - "sync" mails from local maildir to IMAP
// - "fetch" mails from IMAP to local maildir folder
// - "move" mail(s) on IMAP server to given folder and message id
// - "fullsync" mails from local maildir to IMAP

import (
	"bufio"
	"crypto/md5"
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

// String funtion dumps Message info
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
	start := time.Now()
	defer timing("connect", start)
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
func imapSnapshot(c *client.Client, imapName, folder string, newMessages bool, writeContent bool) []Message {
	start := time.Now()
	defer timing("imapSnapshot", start)

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
	var mdict map[string]string
	// section will be used only in writeContent
	section := &imap.BodySectionName{}

	messages := make(chan *imap.Message, nmsg)
	items := []imap.FetchItem{imap.FetchFlags, imap.FetchEnvelope}
	if writeContent {
		mdict = readMaildir(imapName, folder)
		items = []imap.FetchItem{section.FetchItem(), imap.FetchFlags, imap.FetchEnvelope}
	}
	done := make(chan error, 1)
	go func() {
		done <- c.Fetch(seqset, items, messages)
	}()

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
		log.Printf("fetch %v (hid %v, message id %v, flags %v) out of %v from %s\n", seqNum, hid, mid, flags, nmsg, imapName)
		if writeContent {
			r := msg.GetBody(section)
			// check if our file has md5 hash init (it was already written before)
			if _, ok := mdict[hid]; ok {
				if Config.Verbose > 1 {
					log.Println("Mail with hash", hid, "already exists")
				}
			} else {
				if newMessages {
					flags = append(flags, imap.RecentFlag)
				}
				wg.Add(1)
				go writeMail(imapName, folder, hid, flags, r, &wg)
			}
		}
		m = Message{MessageId: mid, Flags: flags, Imap: imapName, Subject: sub, SeqNumber: seqNum, HashId: hid}
		msgs = append(msgs, m)
		seqNum += 1
	}
	if writeContent {
		wg.Wait()
	}
	return msgs
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

// helper function to write emails in imapName folder of local maildir
func writeMail(imapName, folder, hid string, flags []string, r io.Reader, wg *sync.WaitGroup) {
	start := time.Now()
	defer timing("writeMail", start)
	defer wg.Done()

	// construct file name
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
	// our file name will have format <tstamp.hid.hostname:2,flags> where hid is messageId hash
	tstamp := time.Now().Unix()
	if Config.Verbose > 0 {
		log.Println("writeMail", tstamp, hid, flags, flag)
	}
	fname := fmt.Sprintf("%d.%s.%s:2,%s", tstamp, hid, hostname, flag)
	if strings.Contains(flag, "N") {
		fname = fmt.Sprintf("%d.%s.%s", tstamp, hid, hostname)
	}
	fdir := localFolder(imapName, folder)
	fpath := fmt.Sprintf("%s/%s/cur/%s", Config.Maildir, fdir, fname)
	if strings.Contains(flag, "N") {
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
	start := time.Now()
	defer timing("MoveMessage", start)
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
		log.Printf("delete %v on %s\n", msg.MessageId, imapName)
	} else {
		log.Printf("move %v to '%s' on %s\n", msg.MessageId, folder, imapName)
	}

	// mark mail as seen in our inbox
	item := imap.FormatFlagsOp(imap.AddFlags, true)
	flags := []interface{}{imap.SeenFlag}
	if err := c.Store(seqset, item, flags, nil); err != nil {
		log.Fatal(err)
	}
	// copy mail to folder
	if folder != "" {
		if err := c.Copy(seqset, folder); err != nil {
			log.Fatal(err)
		}
	}
	// mark mail as deleted on IMAP server
	flags = []interface{}{imap.DeletedFlag}
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
	start := time.Now()
	defer timing("Move", start)
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
	done := make(chan error, 1)
	go func() {
		done <- c.Fetch(seqset, items, messages)
	}()

	seqNum := uint32(1)
	for msg := range messages {
		if Config.Verbose > 1 {
			log.Println("* "+msg.Envelope.Subject+" MessageId ", msg.Envelope.MessageId)
		}
		if msg.Envelope.MessageId == match {
			if Config.Verbose > 0 {
				log.Printf("Found match: seq:%v Envelope: %+v Flags: %+v SeqNum: %v\n", seqNum, msg.Envelope, msg.Flags)
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
	start := time.Now()
	defer timing("Fetch", start)
	log.Printf("Fetch %s from %s\n", folder, imapName)
	for _, m := range imapSnapshot(c, imapName, folder, newMessages, true) {
		if Config.Verbose > 0 {
			log.Println("fetch", m.String())
		}
	}
}

// helper function to sync give mail list on IMAP servers
func syncMails(cmap map[string]*client.Client, mlist []Message, dryRun bool) {
	start := time.Now()
	defer timing("syncMails", start)
	for name, client := range cmap {
		if Config.Verbose > 0 {
			log.Println("Perform sync", name)
		}
		// now we loop over messages which are not present in local list
		// and expunge them from IMAP
		for _, m := range mlist {
			if m.Imap == name {
				if !dryRun {
					// to delete message we'll call move with empty folder
					MoveMessage(client, m.Imap, m, "")
				} else {
					log.Println("dry-run expunge", m.String())
				}
			}
		}
	}
}

// Sync provides sync between local maildir and IMAP servers
func Sync(cmap map[string]*client.Client, dryRun bool) {
	start := time.Now()
	defer timing("Sync", start)

	// get local maildir snapshot
	var mdict map[string]string
	if Config.CommonInbox {
		mdict = readMaildir("", "INBOX")
	}

	// get imap snapshot and compose mail list to sync
	var mlist []Message
	mch := make(chan []Message, len(cmap))
	for name, c := range cmap {
		log.Printf("Sync maildir and %s\n", name)
		if !Config.CommonInbox { // if we use separate inbox folders for our imaps
			mdict = readMaildir(name, "INBOX")
		}
		go func(cl *client.Client, n string) {
			mch <- imapSnapshot(cl, n, "INBOX", false, false)
		}(c, name)
	}
	// collect our message list
	for range cmap {
		msgList := <-mch
		for _, m := range msgList {
			if _, ok := mdict[m.HashId]; !ok {
				mlist = append(mlist, m)
			}
		}
	}
	if Config.Verbose > 0 {
		log.Println("We need to sync", len(mlist), "mails")
	}
	syncMails(cmap, mlist, dryRun)
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
	// parse config structure
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
		fmt.Println("   fullsync : to fetch new messages and then sync local maildir with IMAP server(s)")
		fmt.Println("Examples:")
		fmt.Println("   # fetch new messages from given IMAP folder")
		fmt.Println("   goimapsync -config config.json -op=fetch-new -folder=MyFolder")
		fmt.Println("   # fetch all messages from given IMAP folder")
		fmt.Println("   goimapsync -config config.json -op=fetch-all -folder=MyFolder")
		fmt.Println("   # sync mails form local maildir to IMAP")
		fmt.Println("   goimapsync -config config.json -op=sync")
		fmt.Println("   # perform full sync, i.e. fetch new messages from INBOX and then sync")
		fmt.Println("   goimapsync -config config.json -op=fullsync")
		fmt.Println("   # the same operation with encrypted (gpg) config")
		fmt.Println("   gpg -d -o - $HOME/.goimapsync.gpg | goimapsync -op=sync -config -")
		fmt.Println("   # sync mails form given IMAP folder into local maildir")
		fmt.Println("   gpg -d -o - $HOME/.goimapsync.gpg | goimapsync -config config.json -op=fetch -folder=MyFolder")
		fmt.Println("   # move given mail id in IMAP server to given folder")
		fmt.Println("   goimapsync -config config.json -op=move -mid=123 -folder=MyFolder")
	}
	flag.Parse()
	err := ParseConfig(config)
	if err != nil {
		log.Println("Unable to parse config file", config, err)
	}
	// overwrite verbose level in config
	if verbose > 0 {
		Config.Verbose = verbose
	}
	// log time, filename, and line number
	if Config.Verbose > 0 {
		log.SetFlags(log.LstdFlags | log.Lshortfile)
	} else {
		log.SetFlags(log.LstdFlags)
	}

	// add timing profile
	start := time.Now()
	defer timing("main", start)

	if Config.CommonInbox {
		log.Printf("maildir: %s, use common inbox for all IMAP servers\n", Config.Maildir)
	} else {
		log.Printf("maildir: %s\n", Config.Maildir)
	}

	// init imap folders map
	imapFolders = make(map[string][]string)
	hostname, err = os.Hostname()

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
	if op == "move" {
		if folder != "" && mid != "" {
			// perform move action for given message id and IMAP folder
			for name, c := range cmap {
				Move(c, name, mid, folder)
			}
		} else {
			log.Fatal("Move operation requires both folder and message id")
		}
	} else if op == "fetch-new" {
		// fetch new messages for given IMAP folder
		for name, c := range cmap {
			Fetch(c, name, folder, true)
		}
	} else if op == "fetch-all" {
		// fetch all messages (old and new) for given IMAP folder
		for name, c := range cmap {
			Fetch(c, name, folder, false)
		}
	} else if op == "sync" {
		// sync emails between local maildir and IMAP server
		Sync(cmap, dryRun)
	} else if op == "fullsync" {
		// fetch new messages from Inbox folder of IMAP servers
		for name, c := range cmap {
			Fetch(c, name, "INBOX", true)
		}
		// sync emails from local maildir to IMAP server
		Sync(cmap, dryRun)
	} else {
		log.Fatalf("Given operation '%s' is not supported, please use sync, fullsync, fetch-new, fetch-all\n", op)
	}
}
