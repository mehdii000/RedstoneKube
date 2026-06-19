package mc.parkour;

import java.util.ArrayList;
import java.util.List;
import java.util.Random;

/** Pure, Bukkit-free course generator + win check (mirrors the stub's Done split). */
public final class Course {
  private Course() {}

  public record Vec3(int x, int y, int z) {}

  private static final int START_Y = 64;

  /** Start platform — fixed so the plugin can place spawn before generating. */
  public static Vec3 start() { return new Vec3(0, START_Y, 0); }

  /**
   * Deterministic course of `length` jumps from the start. Each step advances
   * +x by a jumpable gap (2-3) with a small sideways (0-1) and vertical (-1..1)
   * offset, so every gap satisfies horizontalStep <= 4 and |dy| <= 1.
   */
  public static List<Vec3> generate(long seed, int length) {
    Random rnd = new Random(seed);
    List<Vec3> out = new ArrayList<>(length + 1);
    Vec3 cur = start();
    out.add(cur);
    for (int i = 0; i < length; i++) {
      int dx = 2 + rnd.nextInt(2);          // 2..3 forward
      int dz = rnd.nextInt(2);              // 0..1 sideways  (dx+dz <= 4)
      if (rnd.nextBoolean()) dz = -dz;
      int dy = rnd.nextInt(3) - 1;          // -1..1
      cur = new Vec3(cur.x() + dx, cur.y() + dy, cur.z() + dz);
      out.add(cur);
    }
    return out;
  }

  public static Vec3 finish(List<Vec3> course) { return course.get(course.size() - 1); }

  /** True when the player is within ~1.5 blocks (horizontally + vertically) of finish. */
  public static boolean atFinish(double px, double py, double pz, Vec3 finish) {
    return Math.abs(px - finish.x()) <= 1.5
        && Math.abs(pz - finish.z()) <= 1.5
        && Math.abs(py - finish.y()) <= 2.0;
  }
}
