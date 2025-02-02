I made this one morning using o3-mini in cursor.

To build, install go lang and run `go mod tidy` to download dependencies. Then `go run *.go` to spin up the server.

Expects env variables 
SLACK_BOT_TOKEN
GOOGLE_GEMINI_API_KEY

And event type "app_mention" enabled for the slack bot.


Example usage:
@SLACKBOTAPP /invite "chris,connor" "cs go but we just open cases"
-> Sends message to users found with fuzzy find. if no user is found, we print out available users.

Conversational guided path exists, message @SLACKBOTAPP to start, and always tag @SLACKBOTAPP to respond.

