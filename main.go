package main

import (
	"encoding/json"
	"github.com/agonzalezro/configura"
	"github.com/boltdb/bolt"
	"github.com/thoj/go-ircevent"
	"log"
	"os"
	"regexp"
	"strings"
	"time"
)

var (
	linkRegex = regexp.MustCompile(`((ftp|git|http|https):\/\/(\w+:{0,1}\w*@)?(\S+)(:[0-9]+)?(?:\/|\/([\w#!:.?+=&%@!\-\/]))?)`)
	timeFmt   = "2 Jan 2006 15:04"
	config    = Config{}
)

type Config struct {
	Host      string
	Channel   string
	Nick      string `configura:",linkeater"`
	LookupCmd string `configura:",^url"`
	RepostMsg string `configura:",Repost:"`
	DB        string `configura:",linkeater.db"`
}

type Link struct {
	Url  string    `json:"url,omitempty"`
	User string    `json:"user,omitempty"`
	Time time.Time `json:"time,omitempty"`
}

func main() {
	err := configura.Load("LE_", &config)
	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}

	db, err := bolt.Open(config.DB, 0600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
	defer db.Close()

	db.Update(func(tx *bolt.Tx) error {
		tx.CreateBucketIfNotExists([]byte("urls"))
		return nil
	})

	log.Print("Connecting")
	c := irc.IRC(config.Nick, config.Nick)
	err = c.Connect(config.Host)
	if err != nil {
		log.Fatalf("Error connecting: %s", err.Error())
		os.Exit(1)
	}
	c.Join(config.Channel)

	c.AddCallback("PRIVMSG", func(e *irc.Event) {
		if e.Arguments[0] == config.Channel {
			message := e.Message()

			if strings.HasPrefix(message, config.LookupCmd) {
				term := message[len(config.LookupCmd):len(message)]
				go seekout(term, c, db)
				return
			}

			if links := linkRegex.FindAllString(e.Message(), -1); links != nil {
				go storelinks(links, e, c, db)
			}
		}
	})

	for {
		time.Sleep(time.Second)
	}
}

func seekout(term string, c *irc.Connection, db *bolt.DB) {
	match := strings.TrimSpace(term)
	var links []Link

	if strings.HasPrefix(match, "/") && strings.HasSuffix(match, "/") {
		links = linksMatchingRegex(match, c, db)
		if len(links) == 0 {
			c.Privmsg(config.Channel, "Ain't found no matching links.")
			return
		}
	} else {
		links = linksMatchingNick(match, db)
		if len(links) == 0 {
			c.Privmsgf(config.Channel, "No links from %s", match)
			return
		}
	}

	for _, link := range links {
		c.Privmsg(config.Channel, link.Url)
	}
}

func linksMatchingRegex(regex string, c *irc.Connection, db *bolt.DB) []Link {
	var links []Link
	r, err := regexp.Compile(regex[1 : len(regex)-1])
	if err != nil {
		c.Privmsg(config.Channel, "That ain't a regex, mang.")
		return links
	}

	db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("urls"))
		b.ForEach(func(k, v []byte) error {
			if r.Match(k) {
				l, err := decodeLink(v)
				if err != nil {
					log.Printf("Could not decode link %s: %s", string(k), err.Error())
					return nil
				}
				links = append(links, l)
			}
			return nil
		})
		return nil
	})
	return links
}

func linksMatchingNick(nick string, db *bolt.DB) []Link {
	var links []Link

	db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(nick))
		if b == nil {
			return nil
		}

		b.ForEach(func(k, v []byte) error {
			l, err := decodeLink(v)
			if err != nil {
				log.Printf("Could not decode link %s: %s", string(k), err.Error())
				return nil
			}
			links = append(links, l)
			return nil
		})

		return nil
	})

	return links
}

func storelinks(links []string, e *irc.Event, c *irc.Connection, db *bolt.DB) {
	db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("urls"))

		for _, link := range links {
			if l := b.Get([]byte(link)); l != nil {
				decoded, err := decodeLink(l)
				if err != nil {
					log.Printf("Error decoding link: %s", err.Error())
					continue
				}

				c.Privmsgf(config.Channel, "%s: %s %s posted that on %s",
					e.Nick,
					config.RepostMsg,
					decoded.User,
					decoded.Time.Format(timeFmt))
			} else {
				log.Printf("Storing link: %s from %s", link, e.Nick)
				encoded, err := encodeLinkFromEvent(link, e)
				if err != nil {
					log.Printf("Error encoding link %s: %s", link, err.Error())
				}
				b.Put([]byte(link), encoded)

				ub, err := tx.CreateBucketIfNotExists([]byte(e.Nick))
				if err == nil {
					ub.Put([]byte(link), encoded)
				}
			}
		}
		return nil
	})
}

func encodeLinkFromEvent(link string, event *irc.Event) ([]byte, error) {
	l := Link{
		Url:  link,
		User: event.Nick,
		Time: time.Now(),
	}

	return json.Marshal(l)
}

func decodeLink(data []byte) (Link, error) {
	var l Link
	err := json.Unmarshal(data, &l)
	return l, err
}
