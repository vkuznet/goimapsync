### goimapsync
The `goimapsync` is a tool to bi-directionally sync local Maildir snapshot to
IMAP servers of your choice. It supports the following set of actions:
- *sync* mails from local maildir to IMAP
- *fetch* mails from IMAP to local maildir folder
- *move* mail(s) on IMAP server to given folder and message id
- *fullsync* mails from local maildir to IMAP
It reproduces functionality of
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

# perform full sync, i.e. first mails from INBOX and then sync local maildir
goimapsync -config config.json -op=fullsync

# the same operation with encrypted (gpg) config
gpgp -d -o $HOME/.goimapsync.gpg | goimapsync -op=fullsync -config -

# move given mail id in IMAP server to given folder
goimapsync -config config.json -op=move -mid=123 -folder=MyFolder
```
