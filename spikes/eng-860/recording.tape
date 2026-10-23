# ENG-860 recording: setup.sh creates the template, vault, agent and session;
# the agent gets masked users through the sidecar lane and a refused DELETE;
# then the sidecar audit rows. The key is set while the tape is hidden.
# Run it from this folder: vhs recording.tape
Output out/recording.gif
Output out/recording.mp4
Set Shell bash
Set FontSize 13
Set Width 1500
Set Height 1150
Set TypingSpeed 15ms
Set WaitTimeout 240s

Hide
Type `export OPENAI_API_KEY=$(cat ~/.hoop/secrets/openai.key) SIDECAR_HOST=$(sed -n 's/^LANE=//p' stack/.work/hosts) CALLER_TOKEN_FILE=stack/.secrets/caller-token START=$(date -u +%FT%TZ) PS1='$ ' && clear`
Enter
Show

Type "# OpenAI-hosted sandbox, restricted to one host: a hoop sidecar http lane ($SIDECAR_HOST)"
Enter
Type "./setup.sh"
Enter
Wait /^\$ *$/
Sleep 4s
Type "clear; # the sidecar audit trail for this session"
Enter
Type "./audit.sh $START"
Enter
Sleep 6s
Type "./setup.sh --delete"
Enter
Wait /^\$ *$/
Sleep 2s
