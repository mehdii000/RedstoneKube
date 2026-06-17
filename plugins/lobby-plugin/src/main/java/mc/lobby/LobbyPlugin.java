package mc.lobby;
import org.bukkit.*;
import org.bukkit.entity.Player;
import org.bukkit.event.*;
import org.bukkit.event.player.*;
import org.bukkit.event.inventory.*;
import org.bukkit.inventory.*;
import org.bukkit.inventory.meta.ItemMeta;
import org.bukkit.plugin.java.JavaPlugin;
import java.util.*;

public final class LobbyPlugin extends JavaPlugin implements Listener {
  private static final String TITLE = ChatColor.AQUA + "Minigames";
  private List<Menu.Entry> entries = List.of();

  @Override public void onEnable() {
    saveDefaultConfig();
    @SuppressWarnings("unchecked")
    var raw = (List<Map<String,Object>>) (List<?>) getConfig().getMapList("minigames");
    entries = Menu.parse(raw);
    getServer().getPluginManager().registerEvents(this, this);
    getServer().getMessenger().registerOutgoingPluginChannel(this, "BungeeCord");
  }

  @EventHandler public void onJoin(PlayerJoinEvent e) {
    Player p = e.getPlayer();
    // ponytail: hardcoded "lobby" world; the default anvil world stays a throwaway main world
    // until ASP exposes a slime-as-main-world override.
    World lobby = Bukkit.getWorld("lobby");
    if (lobby != null) p.teleport(lobby.getSpawnLocation());
    p.setInvulnerable(true);
    p.setAllowFlight(true);
    p.setFlying(true);
    ItemStack compass = new ItemStack(Material.COMPASS);
    ItemMeta meta = compass.getItemMeta();
    meta.setDisplayName(ChatColor.AQUA + "Minigames " + ChatColor.GRAY + "(right-click)");
    compass.setItemMeta(meta);
    p.getInventory().setItem(0, compass);
  }

  @EventHandler public void onUse(PlayerInteractEvent e) {
    if (e.getItem() == null || e.getItem().getType() != Material.COMPASS) return;
    if (!e.getAction().name().startsWith("RIGHT_CLICK")) return;
    e.setCancelled(true);
    int rows = Math.max(1, (entries.size() + 8) / 9);
    Inventory inv = Bukkit.createInventory(null, rows * 9, TITLE);
    for (Menu.Entry en : entries) {
      Material mat = Material.matchMaterial(en.material());
      ItemStack it = new ItemStack(mat == null ? Material.PAPER : mat);
      ItemMeta m = it.getItemMeta();
      m.setDisplayName(ChatColor.YELLOW + en.name());
      it.setItemMeta(m);
      inv.addItem(it);
    }
    e.getPlayer().openInventory(inv);
  }

  @EventHandler public void onClick(InventoryClickEvent e) {
    if (!TITLE.equals(e.getView().getTitle())) return;
    e.setCancelled(true);
    if (e.getCurrentItem() == null) return;
    int slot = e.getRawSlot();
    if (slot < 0 || slot >= entries.size()) return;
    String game = entries.get(slot).target();
    Player p = (Player) e.getWhoClicked();
    p.closeInventory();
    String base = getConfig().getString("controller", "http://controller.mc.svc.cluster.local:8080");
    // async: never block the main thread on the allocate HTTP call.
    getServer().getScheduler().runTaskAsynchronously(this, () -> {
      String server = allocate(base, game);
      if (server == null) {
        p.sendMessage(ChatColor.RED + "No " + game + " server available, try again.");
        return;
      }
      getServer().getScheduler().runTask(this, () -> connect(p, server));
    });
  }

  private String allocate(String base, String game) {
    try {
      var resp = java.net.http.HttpClient.newHttpClient().send(
          java.net.http.HttpRequest.newBuilder(java.net.URI.create(base + "/allocate"))
              .header("Content-Type", "application/json")
              .POST(java.net.http.HttpRequest.BodyPublishers.ofString("{\"game\":\"" + game + "\"}"))
              .build(),
          java.net.http.HttpResponse.BodyHandlers.ofString());
      return resp.statusCode() == 200 ? Allocate.parseServer(resp.body()) : null;
    } catch (Exception ex) {
      getLogger().warning("allocate failed: " + ex.getMessage());
      return null;
    }
  }

  private void connect(Player p, String server) {
    com.google.common.io.ByteArrayDataOutput out = com.google.common.io.ByteStreams.newDataOutput();
    out.writeUTF("Connect");
    out.writeUTF(server);
    p.sendPluginMessage(this, "BungeeCord", out.toByteArray());
  }
}
