# sadbot-bot
This is a Discord bot that I made for a server that I'm in. This repo isn't super userful, but it takes advantage of both discordgo and Google Cloud Storage so I'm posting it so people can use it as a reference if they're trying to do something similar. The problem we have is that when someone joins a voice channel, the Discord client doesn't always play the join sound, which leads to:
> "Woah X, when did you get here?"\
> —"What do you mean? I've been here for 13 minutes..."

What this bot does is join and play a custom sound for a user whenever *they* join, so you'll always know when and who joined. This project consists of two parts, the website and the bot. The website works by giving users an interface to customize and submit their "join sound". For more info on the website, check the repo here. When the user submits their join sound, a webhook posts into a specified **read-only** "vote" channel.

The second part (the bot) piggybacks off of the webhook message. If the sound is upvoted by a certain configurable number of people, it is set as their new sound. If it gets enough downvotes, the vote is deleted.

# Configuring
Modify `bin/config.yml` with the corresponding values. Upon launching, the bot will validate both Discord and GC credentials.

# Building
A `.vscode/tasks.json` is included for building the executable. It can also be built using `go build main.go`. and moving `main` to `bin/`.
> NOTE: Building discordgo requires gcc in your PATH. If on Windows, I recommend [TDM](https://jmeubank.github.io/tdm-gcc/) as a "one-click" installer.
