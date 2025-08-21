import ssl
import sys
import traceback

# --- WARNING: THIS SCRIPT DISABLES SSL VERIFICATION --- #
# ---          USE ONLY IF YOU TRUST YOUR NETWORK     --- #
# ---      AND `camoufox fetch` FAILS DUE TO SSL      --- #

print("="*60)
print("WARNING: This script will temporarily disable SSL certificate verification")
print("         globally for this Python process to attempt fetching Camoufox data.")
print("         This can expose you to security risks like man-in-the-middle attacks.")
print("="*60)

confirm = input("Do you understand the risks and want to proceed? (yes/NO): ").strip().lower()

if confirm != 'yes':
    print("Operation cancelled by user.")
    sys.exit(0)

print("\nAttempting to disable SSL verification...")
original_ssl_context = None
try:
    # Store the original context creation function
    if hasattr(ssl, '_create_default_https_context'):
        original_ssl_context = ssl._create_default_https_context
    
    # Get the unverified context creation function
    _create_unverified_https_context = ssl._create_unverified_context
    
    # Monkey patch the default context creation
    ssl._create_default_https_context = _create_unverified_https_context
    print("SSL verification temporarily disabled for this process.")
except AttributeError:
    print("ERROR: Cannot disable SSL verification on this Python version (missing necessary SSL functions).")
    sys.exit(1)
except Exception as e:
    print(f"ERROR: An unexpected error occurred while trying to disable SSL verification: {e}")
    traceback.print_exc()
    sys.exit(1)

# Now, try to import and run the fetch command logic from camoufox
print("\nAttempting to run Camoufox fetch logic...")
fetch_success = False
try:
    # The exact way to trigger fetch programmatically might differ.
    # This tries to import the CLI module and run the fetch command.
    from camoufox import cli
    # Simulate command line arguments: ['fetch']
    # Note: cli.cli() might exit the process directly on completion or error.
    # We assume it might raise an exception or return normally.
    cli.cli(['fetch']) 
    print("Camoufox fetch process seems to have completed.")
    # We assume success if no exception was raised and the process didn't exit.
    # A more robust check would involve verifying the downloaded files, 
    # but that's beyond the scope of this simple script.
    fetch_success = True 
except ImportError:
    print("\nERROR: Could not import camoufox.cli. Make sure camoufox package is installed.")
    print("       Try running: pip show camoufox")
except FileNotFoundError as e:
     print(f"\nERROR during fetch (FileNotFoundError): {e}")
     print("       This might indicate issues with file paths or permissions during download/extraction.")
     print("       Please check network connectivity and directory write permissions.")
except SystemExit as e:
    # The CLI might use sys.exit(). We interpret non-zero exit codes as failure.
    if e.code == 0:
        print("Camoufox fetch process exited successfully (code 0).")
        fetch_success = True
    else:
        print(f"\nERROR: Camoufox fetch process exited with error code: {e.code}")
except Exception as e:
    print(f"\nERROR: An unexpected error occurred while running camoufox fetch: {e}")
    traceback.print_exc()
finally:
    # Attempt to restore the original SSL context
    if original_ssl_context:
        try:
            ssl._create_default_https_context = original_ssl_context
            print("\nOriginal SSL context restored.")
        except Exception as restore_e:
            print(f"\nWarning: Failed to restore original SSL context: {restore_e}")
    else:
        # If we couldn't store the original, we can't restore it.
        # The effect was process-local anyway.
        pass 
        
if fetch_success:
    print("\nFetch attempt finished. Please verify if Camoufox browser files were downloaded successfully.")
else:
    print("\nFetch attempt failed or exited with an error.")

print("Script finished.")
