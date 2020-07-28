package main

// goimapsync module
//
// Copyright (c) 2020 - Valentin Kuznetsov <vkuznet AT gmail dot com>
//

import (
	"bufio"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"

	imap "github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

// extract flags from given email file name
func getFlags(fname string) []string {
	// example of file name in our Inbox
	// /path/Maildir/Inbox/cur/1595417113.48282_0.hostname:2,S
	if strings.Contains(fname, ",") {
		arr := strings.Split(fname, ",")
		flags := arr[len(arr)-1]
		return strings.Split(flags, "")
	}
	return []string{}
}

// Message structure
type Message struct {
	Uid       string   // message Uid
	Path      string   // location of the message in local file dir
	MessageId string   // message Id
	Flags     []string // message Flags
	Imap      string   // name of imap server it belongs to
}

func (m *Message) String() string {
	return fmt.Sprintf("<Imap:%s Uid:%s Path:%s MessageId:%s Flags:%v>", m.Imap, m.Uid, m.Path, m.MessageId, m.Flags)
}

// ServerClient structure
type ServerClient struct {
	Name   string         // name of IMAP server
	Client *client.Client // connected client to IMAP server
}

// extract message id from given email file
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
			// Login
			if err := c.Login(s.Username, s.Password); err != nil {
				log.Fatal(err)
			}
			if Config.Verbose > 0 {
				log.Println("Logged into", s.Uri)
			}
			// TODO: how to handle failed servers
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

// ImapShapshot function takes a snapshot of remote IMAP servers
// and return list of messages
func ImapSnapshot(name string, c *client.Client) []Message {
	// Select given mailbox INBOX
	mbox, err := c.Select("INBOX", false)
	if err != nil {
		log.Fatal(err)
	}

	// Get messages
	from := uint32(1)
	to := mbox.Messages
	seqset := new(imap.SeqSet)
	seqset.AddRange(from, to)
	if Config.Verbose > 0 {
		log.Println("Fetch from IMAP", name)
	}

	messages := make(chan *imap.Message, to)
	items := []imap.FetchItem{imap.FetchFlags, imap.FetchUid, imap.FetchEnvelope}
	done := make(chan error, 1)
	go func() {
		done <- c.Fetch(seqset, items, messages)
	}()

	if Config.Verbose > 1 {
		log.Println("IMAP messages:")
	}
	var msgs []Message
	for msg := range messages {
		uid := fmt.Sprintf("%d", msg.Uid)
		mid := msg.Envelope.MessageId
		flags := msg.Flags
		m := Message{Uid: uid, MessageId: mid, Flags: flags, Imap: name}
		msgs = append(msgs, m)
	}
	return msgs
}

// ReadMaildir reads local maildir area and return message dict
func ReadMaildir() map[string]Message {
	mdict := make(map[string]Message)
	// find inbox folder
	var inbox string
	files, err := ioutil.ReadDir(Config.Maildir)
	if err != nil {
		log.Fatal(err)
	}
	for _, f := range files {
		if strings.ToLower(f.Name()) == "inbox" {
			inbox = fmt.Sprintf("%s/%s", Config.Maildir, f.Name())
		}
	}
	// loop over local maildir
	var flist []string
	err = filepath.Walk(inbox,
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info, err := os.Stat(path); err == nil {
				if !info.IsDir() {
					flist = append(flist, path)
				}
			}
			return nil
		})
	if err != nil {
		log.Fatal(err)
	}
	// concurrent process files from our file list
	ch := make(chan Message, len(flist))
	defer close(ch)
	for _, fname := range flist {
		go func(f string) {
			mid := getMessageId(f)
			msg := Message{Uid: f, MessageId: mid, Flags: getFlags(f)}
			ch <- msg
		}(fname)
	}
	for i := 0; i < len(flist); i++ {
		msg := <-ch
		mdict[msg.MessageId] = msg
		if Config.Verbose > 1 {
			log.Println(msg)
		}
	}
	return mdict
}

// helper function to get folder name for given IMAP server
func getFolder(imapName, folder string) string {
	if folder == "" {
		return folder
	}
	for _, srv := range Config.Servers {
		if srv.Name == imapName {
			for _, f := range srv.Folders {
				if strings.ToLower(f) == strings.ToLower(folder) {
					return f
				}
			}
		}
	}
	// defaults
	if strings.ToLower(folder) == "inbox" {
		return "INBOX"
	}
	if strings.ToLower(folder) == "spam" {
		return "Spam"
	}
	return ""
}

// Move message on IMAP to a given folder, if folder name is not given the mail
// will be deleted
func Move(c *client.Client, imapName, match, folderName string) {
	// inbox folder
	inboxFolder := getFolder(imapName, "inbox")
	folder := getFolder(imapName, folderName)

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
		log.Println("Fetch from IMAP", from, to)
	}

	messages := make(chan *imap.Message, to)
	items := []imap.FetchItem{imap.FetchFlags, imap.FetchUid, imap.FetchEnvelope}
	done := make(chan error, 1)
	go func() {
		done <- c.Fetch(seqset, items, messages)
	}()

	seqNum := uint32(1)
	found := false
	for msg := range messages {
		if Config.Verbose > 1 {
			log.Println("* "+msg.Envelope.Subject+" MessageId ", msg.Envelope.MessageId)
		}
		if msg.Envelope.MessageId == match {
			if Config.Verbose > 0 {
				log.Printf("Found match: seq:%v UID: %v, Envelope: %+v, Flags: %+v\n", seqNum, msg.Uid, msg.Envelope, msg.Flags)
			}
			found = true
			break
		}
		seqNum += 1
	}
	if found {
		// since we found a message we can delete it
		seqset := new(imap.SeqSet)
		seqset.AddNum(seqNum)
		log.Printf("message: %s sequence: %v will move to '%s'", match, seqNum, folder)

		// connect to given Spam folder
		_, err := c.Select(inboxFolder, false)
		if err != nil {
			log.Fatal(err)
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
}

// helper function to sync give mail list on IMAP servers
func syncMails(cmap map[string]*client.Client, mlist []Message, dryRun bool) {
	for name, client := range cmap {
		if Config.Verbose > 0 {
			log.Println("### Perform sync", name)
		}
		// now we loop over messages which are not present in local list
		// and expunge them from IMAP
		for _, m := range mlist {
			if m.Imap == name {
				if Config.Verbose > 0 {
					log.Printf("expunge: %s\n", m.String())
				}
				if !dryRun {
					// to delete message we'll call move with empty folder
					Move(client, m.Imap, m.MessageId, "")
				}
			}
		}
	}
}

// Sync provides sync between local maildir and IMAP servers
func Sync(cmap map[string]*client.Client, dryRun bool) {

	// get local maildir snapshot
	mdict := ReadMaildir()

	// get imap snapshot and compose mail list to sync
	var mlist []Message
	for name, client := range cmap {
		for _, m := range ImapSnapshot(name, client) {
			// if imap message is not found in local maildir dict
			// we'll keep it for deletion
			if Config.Verbose > 1 {
				log.Println(m.String())
			}
			if _, ok := mdict[m.MessageId]; !ok {
				mlist = append(mlist, m)
			}
		}
	}
	syncMails(cmap, mlist, dryRun)
}

func main() {
	var config string
	flag.StringVar(&config, "config", os.Getenv("HOME")+"/.goimapsyncrc", "config JSON file")
	var dryRun bool
	flag.BoolVar(&dryRun, "dryRun", false, "perform dry-run")
	var mid string
	flag.StringVar(&mid, "mid", "", "mail file or messageid to use")
	var folder string
	flag.StringVar(&folder, "folder", "", "folder folder to use")
	flag.Parse()
	err := ParseConfig(config)
	if err != nil {
		log.Println("Unable to parse config file", config, err)
	}
	// log time, filename, and line number
	if Config.Verbose > 0 {
		log.SetFlags(log.LstdFlags | log.Lshortfile)
	} else {
		log.SetFlags(log.LstdFlags)
	}

	// connect to our IMAP servers
	cmap := connect()
	defer logout(cmap)

	// perform move action for given message id and IMAP folder
	if folder != "" && mid != "" {
		for name, c := range cmap {
			Move(c, name, mid, folder)
		}
		os.Exit(0)
	}
	Sync(cmap, dryRun)
}
