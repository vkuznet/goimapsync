### goimapsync
The `goimapsync` is a tool to sync local Maildir snapshot to IMAP servers of
your choice. Why do we need this? Currently, with mutt email client
we have different choices, e.g. a popular [offlimeimap](https://github.com/OfflineIMAP/offlineimap)
python based tool. But it has high level of complexity, e.g. 
it combines read and sync functions, it is Python (meaning it can
be slow), it is not bullet proof, etc.

Instead, I want something simpler and more reliable. For instance, we may use
the following setup for mutt:
- [fetchmail](https://www.fetchmail.info/)
- [procmail](https://userpages.umbc.edu/~ian/procmail.html)
- [neomutt](https://neomutt.org/)

Each tool is very well designed to do one task (K.I.S.S unix principle),
the fetchmail fetches the mails from IMAP servers of your choice,
the procmail redirects them to your Maildir, while (neo)mutt provide you
terminal UI. What is missing here is a sync tool which will allow to
sync your local Maildir setup back to IMAPs.

This was the idea behind the `goimapsync`. The tool is based on
[go-imap](github.com/emersion/go-imap) middleware, and so far is a
work in progress. Its logic is simple:
- fetch content of your folder(s) from local maildir
- fetch IMAP content from your folder(s)
- compare the two
- update your IMAP server(s) based on your local Maildir state

All of this functionality is implemented but I didn't yet enable final step.
