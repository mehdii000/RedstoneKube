package mc.parkour;

import org.junit.jupiter.api.Test;
import java.util.List;
import static org.junit.jupiter.api.Assertions.*;

class CourseTest {
  @Test void deterministicForSameSeed() {
    assertEquals(Course.generate(42L, 10), Course.generate(42L, 10));
  }

  @Test void lengthIsPlatformCountPlusStart() {
    assertEquals(11, Course.generate(1L, 10).size()); // start + 10 steps
  }

  @Test void everyGapIsJumpable() {
    // Consecutive platforms must be reachable: horizontal step <= 4, |dy| <= 1.
    for (long seed = 0; seed < 50; seed++) {
      List<Course.Vec3> c = Course.generate(seed, 30);
      for (int i = 1; i < c.size(); i++) {
        Course.Vec3 a = c.get(i - 1), b = c.get(i);
        int dx = Math.abs(b.x() - a.x()), dz = Math.abs(b.z() - a.z());
        assertTrue(dx + dz >= 1 && dx + dz <= 4, "seed " + seed + " step " + i + " gap " + (dx + dz));
        assertTrue(Math.abs(b.y() - a.y()) <= 1, "seed " + seed + " step " + i + " dy");
      }
    }
  }

  @Test void atFinishWithinThreshold() {
    Course.Vec3 f = new Course.Vec3(10, 64, 3);
    assertTrue(Course.atFinish(10.4, 64.0, 3.2, f));
    assertFalse(Course.atFinish(7.0, 64.0, 3.0, f));
  }

  @Test void doneUrlTrimsSlash() {
    assertEquals("http://c:8080/instances/x/done", Done.doneUrl("http://c:8080/", "x"));
  }
}
