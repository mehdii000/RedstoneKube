package mc.metrics;

import com.sun.net.httpserver.HttpServer;
import org.bukkit.Bukkit;
import org.bukkit.plugin.java.JavaPlugin;
import java.io.IOException;
import java.io.OutputStream;
import java.lang.management.ManagementFactory;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;

public final class MetricsPlugin extends JavaPlugin {
  private HttpServer http;
  private long enableMillis;

  @Override public void onEnable() {
    enableMillis = System.currentTimeMillis();
    final double jvmStartupSec =
        (enableMillis - ManagementFactory.getRuntimeMXBean().getStartTime()) / 1000.0;
    try {
      http = HttpServer.create(new InetSocketAddress("0.0.0.0", 9100), 0);
    } catch (IOException e) {
      getLogger().severe("metrics server failed to bind 9100: " + e);
      return;
    }
    http.createContext("/metrics", ex -> {
      byte[] body;
      try {
        double tps = Bukkit.getTPS()[0];
        double mspt = Bukkit.getAverageTickTime();
        int players = Bukkit.getOnlinePlayers().size();
        int max = Bukkit.getMaxPlayers();
        long uptime = (System.currentTimeMillis() - enableMillis) / 1000;
        body = MetricsJson.build(tps, mspt, players, max, uptime, jvmStartupSec)
            .getBytes(StandardCharsets.UTF_8);
      } catch (Throwable t) {
        getLogger().warning("metrics render failed: " + t);
        ex.sendResponseHeaders(500, -1);
        ex.close();
        return;
      }
      ex.getResponseHeaders().set("Content-Type", "application/json");
      ex.sendResponseHeaders(200, body.length);
      try (OutputStream os = ex.getResponseBody()) { os.write(body); }
    });
    http.start();
    getLogger().info("metrics exporter on :9100");
  }

  @Override public void onDisable() {
    if (http != null) http.stop(0);
  }
}
