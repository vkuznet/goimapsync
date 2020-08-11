goimapsync
==========

[![Build Status](https://travis-ci.org/vkuznet/goimapsync.svg?branch=master)](https://travis-ci.org/vkuznet/goimapsync)
[![Go Report Card](https://goreportcard.com/badge/github.com/vkuznet/goimapsync)](https://goreportcard.com/report/github.com/vkuznet/goimapsync)
[![GoDoc](https://godoc.org/github.com/vkuznet/goimapsync?status.svg)](https://godoc.org/github.com/vkuznet/goimapsync)

`goimapsync` is a tool to synchronize IMAP server(s) with local maildir.

### Introduction

The `goimapsync` is a tool to bi-directionally sync local Maildir snapshot to
IMAP servers of your choice. It supports the following set of actions:
- *sync*      to fetch and sync local maildir with IMAP server(s)
- *fetch-new* to fetch new messages from IMAP
- *fetch-all* to fetch all messages from IMAP
- *move*      to move mail(s) on IMAP server to given folder and message id,
  e.g. move message on IMAP to Spam folder
It reproduces (some) functionality of
[fetchmail](https://www.fetchmail.info/),
[procmail](https://userpages.umbc.edu/~ian/procmail.html)
[offlineimap](https://github.com/OfflineIMAP/offlineimap), or
similary tools and intent to work as main engine for
[mutt](http://www.mutt.org/) Email client.

Even though these tools work great there are certain level of
expertise is required to setup properly a working environment.
The `goimapsync` is Go-based tool, with build-in concurrentcy
which can compile into static executable and provide easy
setup for mutt.

### Configuration and setup
To setup everything with mutt email client you only need to get
a binary file and instruct mutt to use it. To build the code just
use [Go-lang](https://golang.org/)::
```
# build the code
git clone https://github.com/vkuznet/goimapsync
cd goimapsync
go build
```

To run the code
```
# get help
goimapsync -help

# fetch mails from given IMAP folder
goimapsync -config config.json -op=fetch -folder=MyFolder

# sync mails form local maildir to IMAP
goimapsync -config config.json -op=sync

# move given mail id in IMAP server to given folder
goimapsync -config config.json -op=move -mid=123 -folder=MyFolder
```

### Configuration
The configuration is rather trivial, please provide your configuration
file using the following structure:
```
{
    "servers": [
        {
            "name": "NameOfYourImapServer, e.g. gmail",
            "URI":"imap.gmail.com:993",
            "Username": "username@gmail.com",
            "Password": "userpassword",
            "useTls": true
        },
        {
            "name": "NameOfYourImapServer, e.g. proton",
            "URI":"127.0.0.1:1143",
            "Username": "username@protonmail.com",
            "Password": "hashstringOfTheProtonBridge",
            "useTls": false
        }
    ],
    "commonInbox": true,
    "maildir": "/some/path/Mail/Test"
}
```
Here, you can specify different IMAP servers, use or not common Inbox (if `commonInbox`
is false the Inbox from individual IMAP servers will be kept separately), and
`useTls` defines either to use or not TLS connection to your IMAP server.
For ProtonMail we can use ProtonBridge on your machine and connect to it
w/o TLS (it will encrypt your outgoing mails anyway).

Next, if you want to encrypt your configuration, just use the following:
```
# define output file
ofile=$HOME/.goimapsync.gpg
# define your gpg key
key=bla-bla-bla
# define your input config file
ifile=config.json
# encrypt your config file
gpg -o $ofile -e -r $key $ifile

# perform sync operation using your encrypted config file
gpg -d -o $HOME/.goimapsync.gpg | goimapsync -op=sync -config -
```
