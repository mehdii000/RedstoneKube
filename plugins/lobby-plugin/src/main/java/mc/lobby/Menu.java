package mc.lobby;
import java.util.*;

/** Pure config -> menu-entry logic, unit-testable without Bukkit. */
public final class Menu {
  public record Entry(String name, String material, String target) {}

  public static List<Entry> parse(List<Map<String,Object>> raw) {
    var out = new ArrayList<Entry>();
    for (var m : raw) {
      Object n = m.get("name"), mat = m.get("material"), t = m.get("target");
      if (n == null || mat == null || t == null) continue; // skip incomplete
      out.add(new Entry(n.toString(), mat.toString(), t.toString()));
    }
    return out;
  }
}
