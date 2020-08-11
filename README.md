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

The `goimapsync` reproduces (some) functionality of
[fetchmail](https://www.fetchmail.info/),
[procmail](https://userpages.umbc.edu/~ian/procmail.html)
[offlineimap](https://github.com/OfflineIMAP/offlineimap), or
similary tools. Even though these tools work great a certain level of
expertise is required to setup properly a working environment.
The `goimapsync` is Go-based tool, with build-in concurrentcy
which can compile into static executable. It should be used mostly
with [mutt](http://www.mutt.org/) Email client to fetch and sync
your emails from IMAP servers and local maildir.

### Configuration and setup
To build the code just use [Go-lang](https://golang.org/):
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

#### goimapsync configuration
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

### Integration with mutt Email client
To setup everything with mutt email client please put your `goimapsync`
executable in your PATH and perform two actions:
- prepare `fetchmail.sh` script to run goimapsync, e.g.
```
#!/bin/bash
gpg -d -o - $HOME/.mutt/goimapsync.gpg | $HOME/bin/goimapsync -op=sync -config - 2>&1 1>& /tmp/goimapsync.log
```
Here I speficy how I'd like to run `goimapsync`

- you may assign mutt key to run it at your convenience, e.g.
you may assign `J` key to mark messages as junk (i.e. move
them to spam folder on your IMAP server)
```
# replace paths to suits your environment
macro index,pager J  "<pipe-entry>cat | grep -i message-id > /Users/vk/.cache/mutt/mail.html && gpg -d -o - $HOME/.mutt/goimapsync.gpg | $HOME/bin/goimapsync -config - -folder=\"Spam\" -mid=/Users/vk/.cache/mutt/mail.html<enter><delete-message>" "mark message as junk"
```
or assign key `o` to fetch and sync your mails
```
macro index o "<sync-mailbox><shell-escape>/Users/vk/bin/fetchmail.sh<enter>" "run fetchmail to sync inbox"
```

Then, fire up your mutt client and enjoy your Emails.
