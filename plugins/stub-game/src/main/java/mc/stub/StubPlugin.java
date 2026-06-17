package mc.stub;

import org.bukkit.*;
import org.bukkit.command.*;
import org.bukkit.entity.Player;
import org.bukkit.event.*;
import org.bukkit.event.player.PlayerJoinEvent;
import org.bukkit.plugin.java.JavaPlugin;
import java.net.URI;
import java.net.http.*;

public final class StubPlugin extends JavaPlugin implements Listener {
  @Override public void onEnable() {
    getServer().getPluginManager().registerEvents(this, this);
  }

  @EventHandler public void onJoin(PlayerJoinEvent e) {
    Player p = e.getPlayer();
    // ponytail: reuse the lobby slime world named "game"; Slice 2 gives each game its own world.
    World w = Bukkit.getWorld("game");
    if (w != null) p.teleport(w.getSpawnLocation());
    p.setInvulnerable(true);
    p.sendMessage(ChatColor.GREEN + "Stub minigame. Op: /endgame to recycle this pod.");
  }

  @Override public boolean onCommand(CommandSender s, Command cmd, String label, String[] args) {
    if (!cmd.getName().equalsIgnoreCase("endgame")) return false;
    if (!s.isOp()) { s.sendMessage("op only"); return true; }
    String url = Done.doneUrl(System.getenv("CONTROLLER_URL"), System.getenv("INSTANCE_ID"));
    s.sendMessage("ending game -> " + url);
    // async: don't block the main thread on a network call.
    getServer().getScheduler().runTaskAsynchronously(this, () -> postDone(url));
    return true;
  }

  private void postDone(String url) {
    try {
      HttpClient.newHttpClient().send(
          HttpRequest.newBuilder(URI.create(url)).POST(HttpRequest.BodyPublishers.noBody()).build(),
          HttpResponse.BodyHandlers.discarding());
    } catch (Exception ex) {
      getLogger().warning("POST done failed: " + ex.getMessage());
    }
  }
}
