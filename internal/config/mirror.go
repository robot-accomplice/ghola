package config

import "regexp"

// MirrorOptions are the options of ghola mirror (v1 spec, mirror group),
// as wget -r / -m has them.
type MirrorOptions struct {
	On          bool     // the job is ghola mirror
	Depth       int      // --depth: link levels to follow; 0 is no limit
	Parent      bool     // --parent: follow links above the start folder
	Hosts       []string // --hosts: the hosts links may go to; empty is the start host
	Accept      []string // --accept: file-name globs to keep
	Reject      []string // --reject: file-name globs to skip
	AcceptRegex *regexp.Regexp
	RejectRegex *regexp.Regexp
	IncludeDirs []string // --include-dirs: folders to follow
	ExcludeDirs []string // --exclude-dirs: folders to skip
	Layout      string   // --layout: full, no-host or flat
	CutDirs     int      // --cut-dirs
	Timestamps  bool     // --timestamps: get a file again only when the server copy is newer
	Requisites  bool     // --requisites: also the images and styles a page needs
	Convert     bool     // --convert-links
	Backup      bool     // --backup-converted
	AdjustExt   bool     // --adjust-extension
	Robots      bool     // --robots: obey robots.txt and nofollow
	Quota       int64    // --quota: stop after this many bytes; 0 is no limit
	Spider      bool     // --spider: check the links and save nothing
	FileNames   string   // --file-names: unix, windows or ascii; "" is the system's
	WARC        string   // --warc: also write the exchange as a WARC file
}
