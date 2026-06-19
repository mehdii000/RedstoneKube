package mc.parkour;

import org.bukkit.*;
import org.bukkit.entity.Player;
import org.bukkit.event.*;
import org.bukkit.event.player.PlayerJoinEvent;
import org.bukkit.event.player.PlayerMoveEvent;
import org.bukkit.generator.ChunkGenerator;
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

    // Runtime-generated void world — no baked .slime (the convention allows either).
    world = new WorldCreator("parkour")
        .generator(new ChunkGenerator() {})   // empty generator => void
        .generateStructures(false)
        .createWorld();

    Material block = Material.QUARTZ_BLOCK;
    for (Course.Vec3 v : course) world.getBlockAt(v.x(), v.y(), v.z()).setType(block);
    world.getBlockAt(finish.x(), finish.y(), finish.z()).setType(Material.EMERALD_BLOCK);

    Course.Vec3 s = Course.start();
    startLoc = new Location(world, s.x() + 0.5, s.y() + 1, s.z() + 0.5);
    floorY = s.y() - 10.0;
    world.setSpawnLocation(startLoc);

    getServer().getPluginManager().registerEvents(this, this);
  }

  @EventHandler public void onJoin(PlayerJoinEvent e) {
    Player p = e.getPlayer();
    p.teleport(startLoc);
    p.sendMessage(ChatColor.AQUA + "Parkour! Reach the green block to win.");
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
