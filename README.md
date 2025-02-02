I made this one morning using o3-mini in cursor.

To build, install go lang and run `go mod tidy` to download dependencies. Then `go run *.go` to spin up the server.

Expects env variables 
SLACK_BOT_TOKEN
GOOGLE_GEMINI_API_KEY

And event type "app_mention" enabled for the slack bot.
