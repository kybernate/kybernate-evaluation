# Run Tests

## Simple criu test

```
# 1. Preparation: Refresh sudo privileges and clean directory
sudo -v
rm -rf ~/criu_quicktest && mkdir ~/criu_quicktest

# 2. Start process (Python one-liner counting up)
# flush=True is important so output appears immediately
python3 -c 'import time, os; i=0; 
while True: print(f"Counter: {i} | PID: {os.getpid()}", flush=True); i+=1; time.sleep(1)' &

# Save PID
PID=$!
echo "--> Process started with PID: $PID. Letting it run for a moment..."

# 3. Wait briefly to verify counting
sleep 5

# 4. DUMP (Create checkpoint & kill process)
echo "--> Creating checkpoint and terminating process..."
sudo criu dump -t $PID -D ~/criu_quicktest --shell-job -v4

echo "--> Process is terminated (Output should have stopped)."
echo "--> Checking status: Process $PID no longer exists."
# Short pause for visual confirmation
sleep 3

# 5. RESTORE (Restore - detached)
echo "--> Restoring process..."
sudo criu restore -D ~/criu_quicktest --shell-job -d -v4

echo "--> Restore successful! The counter should continue exactly where it left off."

# 6. Let process run for observation, then clean up
sleep 5
echo "--> Test finished. Killing the process finally."
kill -9 $PID
```

## Simple checkpoint tests

* https://github.com/NVIDIA/cuda-checkpoint

Run the counter test

```
cd ~/cuda-checkpoint/src/

# Compile the counter example (just in case)
nvcc counter.cu -o counter

# 1. Preparation
sudo killall -9 counter 2>/dev/null
rm -rf demo/
mkdir -p demo
rm -f result_*.txt

# 2. Start Server
echo "--> Starting Counter..."
./counter &
PID=$!
echo "Process running (PID $PID). Waiting 5s for CUDA Init..."
sleep 5

# DEFINITION: The Python Client Command (sends 'ping', waits 3s for reply)
PY_CLIENT="import socket; s=socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.settimeout(3); s.sendto(b'ping', ('127.0.0.1', 10000)); print(s.recvfrom(1024)[0].decode().strip())"

# 3. Test BEFORE Checkpoint
echo "--> Sending Request 1 (via Python)..."
python3 -c "$PY_CLIENT" > result_1.txt 2>error_1.log
echo "Response 1 received: '$(cat result_1.txt)'"

# 4. CHECKPOINT DUMP
echo "--> Starting Checkpoint..."
sudo cuda-checkpoint --toggle --pid $PID
sudo criu dump --shell-job --images-dir demo --tree $PID

echo "--> Process has been terminated."
sleep 2

# 5. RESTORE
echo "--> Restoring..."
sudo criu restore --shell-job --restore-detached --images-dir demo
sudo cuda-checkpoint --toggle --pid $PID

echo "--> Restore finished. Waiting briefly..."
sleep 2

# 6. Test AFTER Restore
echo "--> Sending Request 2 (via Python)..."
python3 -c "$PY_CLIENT" > result_2.txt 2>error_2.log
echo "Response 2 received: '$(cat result_2.txt)'"

echo "------------------------------------------------"
echo "FINAL RESULT:"
echo "Before:  $(cat result_1.txt)"
echo "After:   $(cat result_2.txt)"
echo "------------------------------------------------"
```

Run the migration tests

```
cd ~/cuda-checkpoint/src/

# API Test
nvcc r580-migration-api.c -o r580-migration-api -lcuda -lnvidia-ml -L/usr/local/cuda/lib64/stubs

./r580-migration-api

# CLI Test
nvcc r580-migration-cli.c -o r580-migration-cli -lcuda -lnvidia-ml -L/usr/local/cuda/lib64/stubs

./r580-migration-cli
```

Expected Outputs are three hashes, where the first and the last hash are equal.

## Run integrated criu GPU workload test

```
# 0. Setup
export CUDA_HOME=/usr/local/cuda
export LD_LIBRARY_PATH=$CUDA_HOME/lib64:$LD_LIBRARY_PATH
export PATH=$CUDA_HOME/bin:$PATH

# CORRECTION: Path to the DIRECTORY, not the specific file!
PLUGIN_DIR=/usr/local/lib/criu

cd ~/cuda-checkpoint/src
# Cleanup
sudo killall -9 counter 2>/dev/null
rm -rf cuda_plugin_dump
mkdir -p cuda_plugin_dump
rm -f result_*.txt dump.log restore.log

# 1. Start
echo "--> Starting Counter..."
./counter &
PID=$!
echo "Process running (PID $PID). Waiting 5s..."
sleep 5

# Python Client for testing
PY_CLIENT="import socket; s=socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.settimeout(5); s.sendto(b'ping', ('127.0.0.1', 10000)); print(s.recvfrom(1024)[0].decode().strip())"

echo "--> Test before Dump..."
python3 -c "$PY_CLIENT" > result_pre.txt 2>error_pre.log
echo "Response: $(cat result_pre.txt)"

# 2. Dump Attempt
echo "--> Starting CRIU DUMP (with Plugin Dir: $PLUGIN_DIR)..."

# We use --lib with the directory path
# We use 'sudo env LD_LIBRARY_PATH=...' to pass the variable safely
if sudo env LD_LIBRARY_PATH=$LD_LIBRARY_PATH criu dump --tree $PID \
     --images-dir cuda_plugin_dump \
     --shell-job \
     --lib $PLUGIN_DIR \
     -v4 -o dump.log; then

    echo "✅ DUMP SUCCESSFUL!"
    
    # Fix permissions so we can read the logs
    sudo chown -R $USER:$USER cuda_plugin_dump

    echo "--> Starting Restore..."
    # Restore also requires the Plugin-Dir
    sudo env LD_LIBRARY_PATH=$LD_LIBRARY_PATH criu restore \
         --images-dir cuda_plugin_dump \
         --shell-job \
         --restore-detached \
         --lib $PLUGIN_DIR \
         -v4 -o restore.log
         
    echo "Restore initiated. Waiting 5s for GPU..."
    sleep 5
    
    echo "--> Test after Restore..."
    python3 -c "$PY_CLIENT" > result_post.txt 2>error_post.log
    echo "Response: $(cat result_post.txt)"
    
    echo "---------------------------------"
    echo "COMPARISON:"
    echo "Before:  $(cat result_pre.txt)"
    echo "After:   $(cat result_post.txt)"
    echo "---------------------------------"

else
    echo "❌ DUMP FAILED!"
    echo "--- LOGFILE ANALYSIS ---"
    sudo grep -iE "error|warn" -C 5 cuda_plugin_dump/dump.log
fi
```
