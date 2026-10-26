// SPDX-License-Identifier: GPL-2.0-only
/*
 * Board ID detection via GPIOs based on resistor strapping
 */

#include <linux/gpio/consumer.h>
#include <linux/init.h>
#include <linux/kernel.h>
#include <linux/kobject.h>
#include <linux/module.h>
#include <linux/of.h>
#include <linux/platform_device.h>
#include <linux/sysfs.h>

static int board_id;
static struct kobject *board_info_kobj;
static struct gpio_desc
    *board_id_gpios[4]; // GPIO descriptors for PI0, PI1, PI2, PI3

typedef struct {
  int board_id_value;
  const char *name;
} board_id_entry_t;

// Mapping of board IDs to their string representations
board_id_entry_t board_id_strings[] = {
  {0x2, "c2_p1"},   // 0010b
  {0x3, "c2_evt"},  // 0011b
  {0x8, "c3_p1"},   // 1000b
  {0x9, "c3_pvt"},  // 1001b
};

// Sysfs function to display the board ID
static ssize_t board_id_show(struct kobject *kobj, struct kobj_attribute *attr,
                             char *buf) {
  return sprintf(buf, "%d\n", board_id);
}

// Sysfs attribute for board ID
static struct kobj_attribute board_id_attribute =
    __ATTR(board_id, 0444, board_id_show, NULL);

// Function to read GPIO values and calculate board ID
static int read_board_id_from_gpio(void) {
  int i, value;

  board_id = 0;
  // Read the GPIO values and stitch together the board ID
  for (i = 0; i < 4; i++) {
    value = gpiod_get_value(board_id_gpios[i]);
    if (value < 0) {
      pr_err("Failed to get value from GPIO %d\n", i);
      return value;
    }
    board_id |= (value << i); // Calculate board ID based on GPIO values
  }

  // Lookup the board ID in the table and print the result
  for (i = 0; i < ARRAY_SIZE(board_id_strings); i++) {
    if (board_id_strings[i].board_id_value == board_id) {
      pr_info("Miner Board ID: %d (%s)\n", board_id, board_id_strings[i].name);
      return 0;
    }
  }

  pr_info("Miner Board ID: %d (Unknown)\n", board_id);

  return 0;
}

// Initialize GPIOs based on device tree information
static int board_id_probe(struct platform_device *pdev) {
  struct device *dev = &pdev->dev;
  int i, ret;

  // Find GPIOs for board ID
  for (i = 0; i < 4; i++) {
    board_id_gpios[i] = devm_gpiod_get_index(dev, "id", i, GPIOD_IN);
    if (IS_ERR(board_id_gpios[i])) {
      pr_err("Failed to get GPIO %d from device tree\n", i);
      return PTR_ERR(board_id_gpios[i]);
    }
  }

  // Read the GPIOs and calculate the board ID
  ret = read_board_id_from_gpio();
  if (ret) {
    pr_err("Failed to read board ID\n");
    return ret;
  }

  // Create a sysfs entry for the board ID
  board_info_kobj = kobject_create_and_add("board_info", kernel_kobj);
  if (!board_info_kobj) {
    pr_err("Failed to create board_info kobject\n");
    return -ENOMEM;
  }

  ret = sysfs_create_file(board_info_kobj, &board_id_attribute.attr);
  if (ret) {
    pr_err("Failed to create sysfs file for board ID\n");
    kobject_put(board_info_kobj);
    return ret;
  }

  return 0;
}

// Cleanup when the module is removed
static int board_id_remove(struct platform_device *pdev) {
  sysfs_remove_file(board_info_kobj, &board_id_attribute.attr);
  kobject_put(board_info_kobj);
  return 0;
}

static const struct of_device_id board_id_of_match[] = {
    {
        .compatible = "miner-board-id",
    },
    {},
};
MODULE_DEVICE_TABLE(of, board_id_of_match);

static struct platform_driver board_id_driver = {
    .probe = board_id_probe,
    .remove = board_id_remove,
    .driver =
        {
            .name = "board-id",
            .of_match_table = board_id_of_match,
        },
};

module_platform_driver(board_id_driver);

// License must be GPL to use various kernel APIs
MODULE_LICENSE("GPL");
MODULE_AUTHOR("Proto Mining SW Team");
MODULE_DESCRIPTION("Miner board ID detection based on GPIOs");
