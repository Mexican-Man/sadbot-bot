package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"cloud.google.com/go/storage"
	"github.com/bwmarrin/dgvoice"
	"github.com/bwmarrin/discordgo"
	"gopkg.in/yaml.v3"
)

// Config loaded from config.yaml
type Config struct {
	Bot struct {
		Token          string `yaml:"token"`
		Status         string `yaml:"status"`
		RequestChannel string `yaml:"requestChannel"`
		VotesRequired  int    `yaml:"votesRequired"`
		JoinTimeout    int    `yaml:"joinTimeout"` // in seconds
		voteQueue      []*discordgo.Message
		mutex          sync.Mutex
		timeout        []string
		vc             *discordgo.VoiceConnection
	} `yaml:"bot"`
	GCStorage struct {
		Bucket      string `yaml:"bucket"`
		JSONKeyFile string `yaml:"jsonKeyFile"` // Optional, go to https://console.cloud.google.com/iam-admin/serviceaccounts to get service account key
	} `yaml:"gcs"`
}

var cfg Config

func main() {
	// Read config
	b, err := os.ReadFile("config.yml")
	if err != nil {
		log.Fatal(err)
	}

	// Parse config
	err = yaml.Unmarshal(b, &cfg)
	if err != nil {
		log.Fatal(err)
	}

	// Create bot, check error
	discord, err := discordgo.New(fmt.Sprintf("Bot %s", cfg.Bot.Token))
	if err != nil {
		log.Fatal(err)
		return
	}

	// Hacky fix attempt to recover discordgo crashing :(
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("stacktrace from panic: \n" + string(debug.Stack()))
		}
	}()

	// Set intents, so we get updates from Discord
	discord.Identify.Intents = discordgo.MakeIntent(discordgo.IntentsAll)

	// Add handlers
	discord.AddHandler(messageReactionAdd)
	discord.AddHandler(ready)
	discord.AddHandler(voiceStateUpdate)
	discord.AddHandler(messageCreate)
	dgvoice.OnError = func(str string, err error) { // Hacky fix
		cfg.Bot.vc.Disconnect()
	}

	// Open websocket to discord
	err = discord.Open()
	if err != nil {
		log.Fatal(err)
		return
	}

	// Cleanly close down the Discord session.
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc
	discord.Close()
}

// Ready event
func ready(s *discordgo.Session, e *discordgo.Ready) {
	// Set status
	if cfg.Bot.Status != "" {
		var act []*discordgo.Activity
		act = append(act, &discordgo.Activity{Name: cfg.Bot.Status, Type: discordgo.ActivityTypeGame, URL: "https://breensquad.ca/discord"})
		idle := 0
		s.UpdateStatusComplex(discordgo.UpdateStatusData{IdleSince: &idle, Activities: act, AFK: false})
	}
	fmt.Println("Ready")

	// Get vote channel messages
	messages, err := s.ChannelMessages(cfg.Bot.RequestChannel, 20, "", "", "")
	if err != nil {
		fmt.Println(err)
		return
	}
	// Filter through each message
	for _, message := range messages {

		if len(message.WebhookID) > 0 {
			if len(message.Reactions) > 0 {
				cfg.Bot.voteQueue = append(cfg.Bot.voteQueue, message)
			} else {
				createVote(s, message)
			}
		}
	}
}

func messageCreate(s *discordgo.Session, e *discordgo.MessageCreate) {
	if e.ChannelID == cfg.Bot.RequestChannel && len(e.Reactions) > 0 && len(e.WebhookID) > 0 && len(e.Mentions) > 0 {
		createVote(s, e.Message)
		fmt.Println("Request by:", e.Message.Mentions[0].Username)
	} else if e.ChannelID == cfg.Bot.RequestChannel {
		fmt.Println(e.WebhookID)
	}
}

func messageReactionAdd(s *discordgo.Session, e *discordgo.MessageReactionAdd) {
	user, _ := s.GuildMember(e.GuildID, e.UserID)
	// DONT CRASH
	if user == nil || user.User == nil || e == nil || &e.Emoji == nil {
		return
	}
	for i, message := range cfg.Bot.voteQueue {
		if message.ID == e.MessageID && message.ChannelID == e.ChannelID {
			temp, _ := s.ChannelMessage(e.ChannelID, e.MessageID)
			up, _ := s.MessageReactions(temp.ChannelID, temp.ID, "👍", 100, "1", "1")
			down, _ := s.MessageReactions(temp.ChannelID, temp.ID, "👎", 100, "1", "1")
			updoots := len(up)
			downdoots := len(down)
			if updoots >= cfg.Bot.VotesRequired || downdoots >= cfg.Bot.VotesRequired {
				s.MessageReactionsRemoveAll(e.ChannelID, e.MessageID)
				var message string // Preinit message for edit up ahead
				id := temp.Mentions[0].ID
				if updoots >= cfg.Bot.VotesRequired {
					// Approved message
					message = "<@" + id + ">, your new sound has been approved!"
					file, err := s.ChannelMessage(e.ChannelID, e.MessageID)
					if err != nil {
						fmt.Println(err)
						return
					}

					// Grab sound from message, save it
					out, err := os.Create("./sounds/" + id + ".mp3")
					defer out.Close()
					resp, err := http.Get(file.Attachments[0].URL)
					if err != nil {
						fmt.Println(err)
						return
					}
					defer resp.Body.Close()
					_, err = io.Copy(out, resp.Body)

					// Upload sound to bucket for when Cole innevitably does an 'rm sad-bot/*' again
					bucket, err := storage.NewClient(context.Background())
					if err != nil {
						fmt.Println(err)
						return
					}
					defer bucket.Close()
					f, err := os.Open("./sounds/" + id + ".mp3")
					wc := bucket.Bucket("intro-sounds").Object(id + ".mp3").NewWriter(context.Background())
					if _, err = io.Copy(wc, f); err != nil {
						fmt.Println(err)
						return
					}
					if err := wc.Close(); err != nil {
						fmt.Println(err)
						return
					}

				} else if downdoots >= cfg.Bot.VotesRequired {
					// Deny message
					message = "<@" + id + ">, your new sound was not approved..."
				}

				// Send message
				reader, _ := os.Open("./sounds/" + id + ".mp3")
				file := discordgo.File{Name: id + ".mp3", ContentType: "audio/mpeg", Reader: reader}
				fileSlice := []*discordgo.File{&file}
				data := discordgo.MessageSend{Content: message, Files: fileSlice}
				s.ChannelMessageSendComplex(e.ChannelID, &data)

				cfg.Bot.voteQueue[i] = cfg.Bot.voteQueue[len(cfg.Bot.voteQueue)-1] // Remove from queue
				cfg.Bot.voteQueue = cfg.Bot.voteQueue[:len(cfg.Bot.voteQueue)-1]
				s.ChannelMessageDelete(e.ChannelID, e.MessageID) // Delete old message
			}

		}
	}
}

func createVote(s *discordgo.Session, m *discordgo.Message) {
	// Check for any immediate unfinished votes, and delete them.
	// We do this in case some smooth-brain had to reclip their sound
	lastMessage, _ := s.ChannelMessages(m.ChannelID, 2, "", "", "")
	if len(m.Mentions) > 0 && len(lastMessage) > 1 && len(lastMessage[1].WebhookID) > 0 && len(lastMessage[1].Mentions) > 0 && lastMessage[1].Mentions[0].ID == m.Mentions[0].ID {
		s.ChannelMessageDelete(lastMessage[1].ChannelID, lastMessage[1].ID)
	}

	// Add message id to list
	cfg.Bot.voteQueue = append(cfg.Bot.voteQueue, m)

	// Add reactions for voting
	err := s.MessageReactionAdd(m.ChannelID, m.ID, "👍")
	err = s.MessageReactionAdd(m.ChannelID, m.ID, "👎")
	if err != nil {
		fmt.Println(err)
		return
	}
}

func voiceStateUpdate(s *discordgo.Session, e *discordgo.VoiceStateUpdate) {
	// Check if user is itself
	if e.UserID == s.State.User.ID {
		if e.BeforeUpdate != nil {
			cfg.Bot.vc, _ = s.ChannelVoiceJoin(e.GuildID, e.ChannelID, false, false)
			cfg.Bot.vc.Disconnect()
		}
		return
	}

	// Check if they are on timeout list
	for _, v := range cfg.Bot.timeout {
		if v == e.UserID {
			return
		}
	}

	// Add user to timeout list
	cfg.Bot.timeout = append(cfg.Bot.timeout, e.UserID)
	time.AfterFunc(time.Duration(cfg.Bot.JoinTimeout)*time.Second, func() {
		for i, v := range cfg.Bot.timeout {
			if v == e.UserID {
				cfg.Bot.timeout = append(cfg.Bot.timeout[:i], cfg.Bot.timeout[i+1:]...)
				break
			}
		}
	})

	// Make sure they're joining and not moving channels
	if e.BeforeUpdate != nil {
		return
	}

	// Mutex
	cfg.Bot.mutex.Lock()
	defer func() { cfg.Bot.mutex.Unlock() }()

	// Check if user has sound on file
	_, err := os.Stat("./sounds/" + e.UserID + ".mp3")
	if os.IsNotExist(err) {
		return
	}

	// Defer disconnect
	defer func() { cfg.Bot.vc.Disconnect() }()

	// Copied this from the internet, supposedly it stops that super annoying RANDOM CRASH ALL THE TIME GRRRR
	cfg.Bot.vc, err = s.ChannelVoiceJoin(e.GuildID, e.ChannelID, false, false)
	if err != nil {
		if err, ok := s.VoiceConnections[e.GuildID]; ok {
			cfg.Bot.vc = s.VoiceConnections[e.GuildID]
			if err != nil {
				log.Println("error connecting:", err)
				return
			}
		} else {
			log.Println("error connecting:", err)
			return
		}
	}

	// Play audio
	dgvoice.PlayAudioFile(cfg.Bot.vc, fmt.Sprintf("./sounds/%s.mp3", e.UserID), make(chan bool))
}

/*
func catchline() string {
	lines := []string{
		"I believe in you!",
		"This one's even better than the last!",
		"You can do it!",
		"Show 'em who's boss!",
		"Good luck!",
		"You have to get " + strconv.Itoa(votesRequired) + " votes to win!"}
	randomIndex := rand.Intn(len(lines))
	return lines[randomIndex]
}*/
