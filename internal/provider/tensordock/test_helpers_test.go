package tensordock

// TestSSHKey is a valid ed25519 SSH public key for use in tests.
// This key passes ssh.ParseAuthorizedKey() validation.
// IMPORTANT: Do not use placeholder keys like "ssh-rsa AAAA..." in tests -
// they will fail SSH key validation.
const TestSSHKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAICZKc67k8xgOtBqKhxpzM0lJl7rLG/dQTqWBCpHLwEJN test@example"

// TestSSHKeyAlternate is another valid SSH key for tests requiring multiple keys.
const TestSSHKeyAlternate = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl user@example.com"
