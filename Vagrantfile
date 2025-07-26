# -*- mode: ruby -*-
# vi: set ft=ruby :

# All Vagrant configuration is done below. The "2" in Vagrant.configure
# configures the configuration version (we support older styles for
# backwards compatibility). Please don't change it unless you know what
# you're doing.
VAGRANTFILE_API_VERSION = "2"

# Every Vagrant development environment requires a box. You can search for
# boxes at https://vagrantcloud.com/search.
BOX_NAME = "ubuntu/jammy64"
BOX_VERSION = "20241002.0.0"


Vagrant.configure(VAGRANTFILE_API_VERSION) do |config|
    # The most common configuration options are documented and commented below.
  # For a complete reference, please see the online documentation at
  # https://docs.vagrantup.com.

	config.vm.box = BOX_NAME
  config.vm.box_version = BOX_VERSION
  # Base VM OS configuration.
  config.vm.boot_timeout = 900
  config.vm.graceful_halt_timeout=100
  # Ensure all vagrant boxes use the same SSH key
  config.ssh.insert_key = false
  # Enable agent forwarding over SSH connections
  config.ssh.forward_agent = true
  # Disable automatic box update checking
  config.vm.box_check_update = false
  # Disable vagrant synced folder
  config.vm.synced_folder ".", "/vagrant", disabled: true, id: "vagrant-root"


	# General VirtualBox VM configuration.
  config.vm.provider :virtualbox do |v|
		v.memory = 512
    v.cpus = 1
    v.linked_clone = true
    v.customize ["modifyvm", :id, "--natdnshostresolver1", "on"]
    v.customize ["modifyvm", :id, "--ioapic", "on"]
    v.customize ["modifyvm", :id, "--cableconnected1", "on"]
    v.customize ["modifyvm", :id, "--uart1", "0x3F8", "4"]
    v.customize ["modifyvm", :id, "--uartmode1", "file", File::NULL]
  end

 ## Vagrant Plugins

 # vagrant-vbguest
 # set auto_update to false, if you do NOT want to check the correct
 # additions version when booting this machine
 if Vagrant.has_plugin?("vagrant-vbguest")
	 config.vbguest.auto_update = false
 end

  # Vagrant Host Manager
  if Vagrant.has_plugin?("vagrant-hostmanager")
  	config.hostmanager.enabled = true
    config.hostmanager.manage_host = true
    config.hostmanager.manage_guest = true
    config.hostmanager.ignore_private_ip = false
    config.hostmanager.include_offline = true
  end

  config.vm.define "mcpspy" do |node|
    node.vm.hostname = "mcpspy"
    node.vm.network "private_network", ip: "192.168.34.10"
    node.vm.synced_folder ".", "/home/vagrant/MCPSpy", type: "rsync",
      rsync__exclude: [".git/"],
      rsync__args: ["--verbose", "--archive", "--delete", "-z", "--copy-links"],
      id: "code"
    node.vm.provider "virtualbox" do |vb|
      vb.memory = 2048
      vb.cpus = 2
    end
  end
end
