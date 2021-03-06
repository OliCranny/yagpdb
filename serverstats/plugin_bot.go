package serverstats

import (
	"fmt"
	log "github.com/Sirupsen/logrus"
	"github.com/fzzy/radix/redis"
	"github.com/jonas747/discordgo"
	"github.com/jonas747/dutil/commandsystem"
	"github.com/jonas747/yagpdb/bot"
	"github.com/jonas747/yagpdb/commands"
	"github.com/jonas747/yagpdb/common"
	"time"
)

func (p *Plugin) InitBot() {
	common.BotSession.AddHandler(bot.CustomGuildMemberAdd(HandleMemberAdd))
	common.BotSession.AddHandler(bot.CustomGuildMemberRemove(HandleMemberRemove))
	common.BotSession.AddHandler(bot.CustomMessageCreate(HandleMessageCreate))

	common.BotSession.AddHandler(bot.CustomPresenceUpdate(HandlePresenceUpdate))
	common.BotSession.AddHandler(bot.CustomGuildCreate(HandleGuildCreate))
	common.BotSession.AddHandler(bot.CustomReady(HandleReady))

	commands.CommandSystem.RegisterCommands(&commands.CustomCommand{
		Key:      "stats_settings_public:",
		Category: commands.CategoryTool,
		Cooldown: 10,
		SimpleCommand: &commandsystem.SimpleCommand{
			Name:        "Stats",
			Description: "Shows server stats (if public stats are enabled)",
		},
		RunFunc: func(parsed *commandsystem.ParsedCommand, client *redis.Client, m *discordgo.MessageCreate) (interface{}, error) {
			stats, err := RetrieveFullStats(client, parsed.Guild.ID)
			if err != nil {
				return "Error retrieving stats", err
			}

			total := 0
			for _, c := range stats.ChannelsHour {
				total += c.Count
			}

			embed := &discordgo.MessageEmbed{
				Title:       "Server stats",
				Description: fmt.Sprintf("[Click here to open in browser](https://%s/public/%s/stats)", common.Conf.Host, parsed.Guild.ID),
				Fields: []*discordgo.MessageEmbedField{
					&discordgo.MessageEmbedField{Name: "Members joined 24h", Value: fmt.Sprint(stats.JoinedDay), Inline: true},
					&discordgo.MessageEmbedField{Name: "Members Left 24h", Value: fmt.Sprint(stats.LeftDay), Inline: true},
					&discordgo.MessageEmbedField{Name: "Total Messages 24h", Value: fmt.Sprint(total), Inline: true},
					&discordgo.MessageEmbedField{Name: "Members Online", Value: fmt.Sprint(stats.Online), Inline: true},
					&discordgo.MessageEmbedField{Name: "Total Members", Value: fmt.Sprint(stats.TotalMembers), Inline: true},
				},
			}

			return embed, nil
		},
	})

}

func HandleReady(s *discordgo.Session, r *discordgo.Ready, client *redis.Client) {
	for _, guild := range r.Guilds {
		if guild.Unavailable {
			continue
		}

		err := ApplyPresences(client, guild.ID, guild.Presences)
		if err != nil {
			log.WithError(err).Error("Failed applying presences")
		}
	}
}

func HandleGuildCreate(s *discordgo.Session, g *discordgo.GuildCreate, client *redis.Client) {
	err := client.Cmd("SET", "guild_stats_num_members:"+g.ID, g.MemberCount).Err
	if err != nil {
		log.WithError(err).Error("Failed Settings member count")
	}
	log.WithField("guild", g.ID).WithField("g_name", g.Name).WithField("member_count", g.MemberCount).Info("Set member count")

	err = ApplyPresences(client, g.ID, g.Presences)
	if err != nil {
		log.WithError(err).Error("Failed applying presences")
	}
}

func HandleMemberAdd(s *discordgo.Session, g *discordgo.GuildMemberAdd, client *redis.Client) {
	err := client.Cmd("ZADD", "guild_stats_members_joined_day:"+g.GuildID, time.Now().Unix(), g.User.ID).Err
	if err != nil {
		log.WithError(err).Error("Failed adding member to stats")
	}

	err = client.Cmd("INCR", "guild_stats_num_members:"+g.GuildID).Err
	if err != nil {
		log.WithError(err).Error("Failed Increasing members")
	}
}

func HandlePresenceUpdate(s *discordgo.Session, p *discordgo.PresenceUpdate, client *redis.Client) {
	if p.Status == "" { // Not a status update
		return
	}

	var err error
	if p.Status == "offline" {
		err = client.Cmd("SREM", "guild_stats_online:"+p.GuildID, p.User.ID).Err
	} else {
		err = client.Cmd("SADD", "guild_stats_online:"+p.GuildID, p.User.ID).Err
	}

	if err != nil {
		log.WithError(err).Error("Failed updating a presence")
	}
}

func HandleMemberRemove(s *discordgo.Session, g *discordgo.GuildMemberRemove, client *redis.Client) {
	err := client.Cmd("ZADD", "guild_stats_members_left_day:"+g.GuildID, time.Now().Unix(), g.User.ID).Err
	if err != nil {
		log.WithError(err).Error("Failed adding member to stats")
	}

	err = client.Cmd("DECR", "guild_stats_num_members:"+g.GuildID).Err
	if err != nil {
		log.WithError(err).Error("Failed decreasing members")
	}
}

func HandleMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate, client *redis.Client) {
	channel, err := s.State.Channel(m.ChannelID)
	if err != nil {
		log.WithError(err).Error("Error retrieving channel from state")
		return
	}
	err = client.Cmd("ZADD", "guild_stats_msg_channel_day:"+channel.GuildID, time.Now().Unix(), channel.ID+":"+m.ID+":"+m.Author.ID).Err
	if err != nil {
		log.WithError(err).Error("Failed adding member to stats")
	}
}

func ApplyPresences(client *redis.Client, guildID string, presences []*discordgo.Presence) error {
	client.Append("DEL", "guild_stats_online:"+guildID)
	count := 1
	for _, p := range presences {
		if p.Status == "offline" {
			continue
		}
		count++
		client.Append("SADD", "guild_stats_online:"+guildID, p.User.ID)
	}

	_, err := common.GetRedisReplies(client, count)
	return err
}
