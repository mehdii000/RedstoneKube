package mc.lobby;
import org.junit.jupiter.api.Test;
import java.util.*;
import static org.junit.jupiter.api.Assertions.*;

class MenuTest {
  @Test void parsesEntriesPreservingOrder() {
    var raw = List.<Map<String,Object>>of(
      Map.of("name","BedWars","material","RED_BED","target","bedwars"),
      Map.of("name","SkyWars","material","FEATHER","target","skywars"));
    var out = Menu.parse(raw);
    assertEquals(2, out.size());
    assertEquals("BedWars", out.get(0).name());
    assertEquals("FEATHER", out.get(1).material());
    assertEquals("skywars", out.get(1).target());
  }
  @Test void skipsEntriesMissingFields() {
    var raw = List.<Map<String,Object>>of(Map.of("name","Broken"));
    assertTrue(Menu.parse(raw).isEmpty());
  }
}
