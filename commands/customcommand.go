package commands

import (
	"errors"
	"fmt"
	log "github.com/Sirupsen/logrus"
	"github.com/fzzy/radix/redis"
	"github.com/jonas747/discordgo"
	"github.com/jonas747/dutil"
	"github.com/jonas747/dutil/commandsystem"
	"github.com/jonas747/yagpdb/common"
	"math/rand"
	"strings"
	"time"
)

type CommandCategory string

const (
	CategoryGeneral    CommandCategory = "General"
	CategoryTool       CommandCategory = "Tools"
	CategoryModeration CommandCategory = "Moderation"
	CategoryFun        CommandCategory = "Misc/Fun"
)

var (
	RKeyCommandCooldown = func(uID, cmd string) string { return "cmd_cd:" + uID + ":" + cmd }
)

// Slight extension to the simplecommand, it will check if the command is enabled in the HandleCommand func
// And invoke a custom handlerfunc with provided redis client
type CustomCommand struct {
	*commandsystem.SimpleCommand
	HideFromCommandsPage bool   // Set to  hide this command from the commands page
	Key                  string // GuildId is appended to the key, e.g if key is "test:", it will check for "test:<guildid>"
	CustomEnabled        bool   // Set to true to handle the enable check itself
	Default              bool   // The default state of this command
	Cooldown             int    // Cooldown in seconds before user can use it again
	Category             CommandCategory
	RunFunc              func(parsed *commandsystem.ParsedCommand, client *redis.Client, m *discordgo.MessageCreate) (interface{}, error)
}

func (cs *CustomCommand) HandleCommand(raw string, source commandsystem.CommandSource, m *discordgo.MessageCreate, s *discordgo.Session) error {
	// Track how long execution of a command took
	started := time.Now()
	defer func() {
		cs.logExecutionTime(time.Since(started), raw, m.Author.Username)
	}()

	if source == commandsystem.CommandSourceDM && !cs.RunInDm {
		return errors.New("Cannot run this command in direct messages")
	}

	client, err := common.RedisPool.Get()
	if err != nil {
		log.WithError(err).Error("Failed retrieving redis client")
		return errors.New("Failed retrieving redis client")
	}
	defer common.RedisPool.Put(client)

	channel, err := s.State.Channel(m.ChannelID)
	if err != nil {
		return err
	}

	var guild *discordgo.Guild
	var autodel bool

	if source != commandsystem.CommandSourceDM {
		guild, err = s.State.Guild(channel.GuildID)
		if err != nil {
			return err
		}

		var enabled bool
		// Check wether it's enabled or not
		enabled, autodel, err = cs.Enabled(client, channel.ID, guild)
		if err != nil {
			s.ChannelMessageSend(channel.ID, "Bot is having issues... contact the junas D:")
			return err
		}

		if !enabled {
			go common.SendTempMessage(common.BotSession, time.Second*10, m.ChannelID, fmt.Sprintf("The %q command is currently disabled on this server or channel. *(Control panel to enable/disable <https://%s>)*", cs.Name, common.Conf.Host))
			return nil
		}
	}

	cdLeft, err := cs.CooldownLeft(client, m.Author.ID)
	if err != nil {
		// Just pretend the cooldown is off...
		log.WithError(err).Error("Failed checking command cooldown")
	}

	if cdLeft > 0 {
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("**%q:** You need to wait %d seconds before you can use the %q command again", m.Author.Username, cdLeft, cs.Name))
		return nil
	}

	parsed, err := cs.ParseCommand(raw, m, s)
	if err != nil {
		s.ChannelMessageSend(m.ChannelID, "Failed parsing command: "+CensorError(err))
		return nil
	}

	parsed.Source = source
	parsed.Channel = channel
	parsed.Guild = guild

	if cs.RunFunc != nil {
		resp, err := cs.RunFunc(parsed, client, m)
		if resp != nil {
			err2 := cs.sendResponse(s, resp, m.Message, autodel)
			if err2 != nil {
				log.WithError(err2).Errorf("Failed sending command response %#v", resp)
				return err2
			}
		}
		if err == nil {
			err = cs.SetCooldown(client, m.Author.ID)
			if err != nil {
				log.WithError(err).Error("Failed setting cooldown")
			}
		}
		return err
	}

	return nil
}

func (cs *CustomCommand) sendResponse(s *discordgo.Session, response interface{}, trigger *discordgo.Message, autodel bool) error {

	var msgs []*discordgo.Message
	var err error

	switch t := response.(type) {
	case error:
		msgs, err = dutil.SplitSendMessage(s, trigger.ChannelID, "Error: "+t.Error())
	case string:
		if t == "" {
			return nil
		}
		msgs, err = dutil.SplitSendMessage(s, trigger.ChannelID, t)
	case *discordgo.MessageEmbed:
		perms := 0
		perms, err = s.State.UserChannelPermissions(s.State.User.ID, trigger.ChannelID)
		if err != nil {
			return err
		}

		if perms&discordgo.PermissionAdministrator != 0 || perms&discordgo.PermissionManageServer != 0 || perms&discordgo.PermissionEmbedLinks != 0 {
			log.Println("Has perms", perms&discordgo.PermissionAdministrator != 0, perms&discordgo.PermissionManageServer != 0, perms&discordgo.PermissionEmbedLinks != 0)
			if t.Color == 0 {
				t.Color = rand.Intn(0xffffff)
			}
			m, e := s.ChannelMessageSendEmbed(trigger.ChannelID, t)
			msgs = []*discordgo.Message{m}
			err = e
		} else {
			// fallback
			msgs, err = dutil.SplitSendMessage(s, trigger.ChannelID, common.FallbackEmbed(t))
		}

	}

	if err != nil {
		return err
	}

	if autodel && len(msgs) > 0 {
		go cs.deleteResponse(append(msgs, trigger))
	}

	return nil
}

func (cs *CustomCommand) logExecutionTime(dur time.Duration, raw string, sender string) {
	log.Infof("Handled Command [%4dms] %s: %s", int(dur.Seconds()*1000), sender, raw)
}

func (cs *CustomCommand) deleteResponse(msgs []*discordgo.Message) {
	ids := make([]string, len(msgs))
	for k, msg := range msgs {
		ids[k] = msg.ID
	}

	if len(ids) < 1 {
		return // ...
	}

	time.Sleep(time.Second * 10)

	// Either do a bulk delete or single delete depending on how big the response was
	if len(ids) > 1 {
		common.BotSession.ChannelMessagesBulkDelete(msgs[0].ChannelID, ids)
	} else {
		common.BotSession.ChannelMessageDelete(msgs[0].ChannelID, msgs[0].ID)
	}
}

// customEnabled returns wether the command is enabled by it's custom key or not
func (cs *CustomCommand) customEnabled(client *redis.Client, guildID string) (bool, error) {
	// No special key so it's automatically enabled
	if cs.Key == "" || cs.CustomEnabled {
		return true, nil
	}

	// Check redis for settings
	reply := client.Cmd("GET", cs.Key+guildID)
	if reply.Err != nil {
		return false, reply.Err
	}

	enabled, _ := reply.Bool()

	if cs.Default {
		enabled = !enabled
	}

	if !enabled {
		return false, nil
	}

	return enabled, nil
}

// Enabled returns wether the command is enabled or not
func (cs *CustomCommand) Enabled(client *redis.Client, channel string, guild *discordgo.Guild) (enabled bool, autodel bool, err error) {
	if cs.HideFromCommandsPage {
		return true, false, nil
	}

	ce, err := cs.customEnabled(client, guild.ID)
	if err != nil {
		return false, false, err
	}
	if !ce {
		return false, false, nil
	}

	config := GetConfig(client, guild.ID, guild.Channels)

	// Check overrides first to see if one was enabled, and if so determine if the command is available
	for _, override := range config.ChannelOverrides {
		if override.Channel == channel {
			if override.OverrideEnabled {
				// Find settings for this command
				for _, cmd := range override.Settings {
					if cmd.Cmd == cs.Name {
						return cmd.CommandEnabled, cmd.AutoDelete, nil
					}
				}

			}
			break
		}
	}

	if cs.Key != "" || cs.CustomEnabled {
		return true, false, nil
	}

	// Return from global settings then
	for _, cmd := range config.Global {
		if cmd.Cmd == cs.Name {
			if cs.Key != "" {
				return true, cmd.AutoDelete, nil
			}

			return cmd.CommandEnabled, cmd.AutoDelete, nil
		}
	}

	return false, false, nil
}

// CooldownLeft returns the number of seconds before a command can be used again
func (cs *CustomCommand) CooldownLeft(client *redis.Client, userID string) (int, error) {
	if cs.Cooldown < 1 || common.Testing {
		return 0, nil
	}

	ttl, err := client.Cmd("TTL", RKeyCommandCooldown(userID, cs.Name)).Int64()
	if ttl < 1 {
		return 0, nil
	}

	return int(ttl), err
}

// SetCooldown sets the cooldown of the command as it's defined in the struct
func (cs *CustomCommand) SetCooldown(client *redis.Client, userID string) error {
	if cs.Cooldown < 1 {
		return nil
	}
	now := time.Now().Unix()
	client.Append("SET", RKeyCommandCooldown(userID, cs.Name), now)
	client.Append("EXPIRE", RKeyCommandCooldown(userID, cs.Name), cs.Cooldown)
	_, err := common.GetRedisReplies(client, 2)
	return err
}

// Keys and other sensitive information shouldnt be sent in error messages, but just in case it is
func CensorError(err error) string {
	toCensor := []string{
		common.BotSession.Token,
		common.Conf.ClientSecret,
	}

	out := err.Error()
	for _, c := range toCensor {
		out = strings.Replace(out, c, "", -1)
	}

	return out
}
