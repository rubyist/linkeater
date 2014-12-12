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
		tx.CreateBucket([]byte("urls"))
		return nil
	})

	log.Print("Connecting")
	c := irc.IRC(config.Nick, config.Nick)
	c.Connect(config.Host)
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
	r, err := regexp.Compile(strings.TrimSpace(term))
	if err != nil {
		c.Privmsg(config.Channel, "That ain't a regex, mang.")
		return
	}

	var links []Link

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

	if len(links) == 0 {
		c.Privmsg(config.Channel, "Ain't found no matching links.")
		return
	}

	for _, link := range links {
		c.Privmsg(config.Channel, link.Url)
	}
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

				c.Privmsgf(config.Channel, "Nice repost, ass. %s already posted that on %s", decoded.User, decoded.Time.Format(timeFmt))
			} else {
				log.Printf("Storing link: %s", link)
				encoded, err := encodeLinkFromEvent(link, e)
				if err != nil {
					log.Printf("Error encoding link %s: %s", link, err.Error())
				}
				b.Put([]byte(link), encoded)
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