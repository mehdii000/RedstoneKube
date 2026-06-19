package mc.parkour;

import com.infernalsuite.asp.api.AdvancedSlimePaperAPI;
import com.infernalsuite.asp.api.world.SlimeWorld;
import com.infernalsuite.asp.api.world.properties.SlimeProperties;
import com.infernalsuite.asp.api.world.properties.SlimePropertyMap;
import org.bukkit.*;
import org.bukkit.entity.Player;
import org.bukkit.event.*;
import org.bukkit.event.player.PlayerJoinEvent;
import org.bukkit.event.player.PlayerMoveEvent;
import org.bukkit.event.player.PlayerRespawnEvent;
import org.bukkit.plugin.java.JavaPlugin;
import java.net.URI;
import java.net.http.*;
import java.util.List;

public final class ParkourPlugin extends JavaPlugin implements Listener {
  private World world;
  private Location startLoc;
  private Course.Vec3 finish;
  private double floorY;
  private volatile boolean finished = false;

  @Override public void onEnable() {
    // ponytail: seed the course from INSTANCE_ID so a pod's course is stable across reloads.
    long seed = String.valueOf(System.getenv("INSTANCE_ID")).hashCode();
    List<Course.Vec3> course = Course.generate(seed, 20);
    finish = Course.finish(course);
    Course.Vec3 s = Course.start();

    // Stateless: an in-memory slime world (null loader => never touches the anvil loader / disk),
    // not a vanilla WorldCreator world. readOnly=false so we can place the course into it.
    SlimePropertyMap props = new SlimePropertyMap();
    props.setValue(SlimeProperties.DIFFICULTY, "peaceful");
    props.setValue(SlimeProperties.SPAWN_X, s.x());
    props.setValue(SlimeProperties.SPAWN_Y, s.y() + 1);
    props.setValue(SlimeProperties.SPAWN_Z, s.z());
    props.setValue(SlimeProperties.ALLOW_MONSTERS, false);
    props.setValue(SlimeProperties.ALLOW_ANIMALS, false);
    props.setValue(SlimeProperties.DEFAULT_BIOME, "minecraft:the_void");
    try {
      AdvancedSlimePaperAPI asp = AdvancedSlimePaperAPI.instance();
      SlimeWorld sw = asp.createEmptyWorld("parkour", false, props, null);
      asp.loadWorld(sw, true);
    } catch (Exception e) {
      throw new RuntimeException("failed to create in-memory parkour world", e);
    }
    world = Bukkit.getWorld("parkour");

    for (Course.Vec3 v : course) world.getBlockAt(v.x(), v.y(), v.z()).setType(Material.QUARTZ_BLOCK);
    world.getBlockAt(finish.x(), finish.y(), finish.z()).setType(Material.EMERALD_BLOCK);

    startLoc = new Location(world, s.x() + 0.5, s.y() + 1, s.z() + 0.5);
    floorY = s.y() - 10.0;
    world.setSpawnLocation(startLoc);

    getServer().getPluginManager().registerEvents(this, this);
  }

  @EventHandler public void onJoin(PlayerJoinEvent e) {
    Player p = e.getPlayer();
    p.teleport(startLoc);
    p.setInvulnerable(true); // never die into the void default world
    p.sendMessage(ChatColor.AQUA + "Parkour! Reach the green block to win.");
  }

  // Belt-and-suspenders: if a player somehow dies, respawn at the start, never the overworld.
  @EventHandler public void onRespawn(PlayerRespawnEvent e) {
    e.setRespawnLocation(startLoc);
  }

  @EventHandler public void onMove(PlayerMoveEvent e) {
    if (finished) return;
    Location l = e.getTo();
    if (l.getY() < floorY) { e.getPlayer().teleport(startLoc); return; }
    if (Course.atFinish(l.getX(), l.getY(), l.getZ(), finish)) win(e.getPlayer());
  }

  private void win(Player p) {
    finished = true;
    p.sendMessage(ChatColor.GREEN + "You win! Recycling...");
    String url = Done.doneUrl(System.getenv("CONTROLLER_URL"), System.getenv("INSTANCE_ID"));
    getServer().getScheduler().runTaskAsynchronously(this, () -> postDone(url));
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
