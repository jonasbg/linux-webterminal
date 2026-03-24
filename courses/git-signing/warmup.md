# Before you begin: Try impersonating someone

Before diving into the investigation, let's prove why commit signing matters.

## Step 1: Create a test repo

```
mkdir /tmp/impersonation-demo && cd /tmp/impersonation-demo
git init
```

## Step 2: Make a commit as yourself

```
git config user.name "Your Name"
git config user.email "you@example.com"
echo "legitimate change" > file.txt
git add file.txt
git commit -m "My real commit"
git log --format="%an <%ae> - %s"
```

## Step 3: Now impersonate someone else

```
git config user.name "Linus Torvalds"
git config user.email "torvalds@linux-foundation.org"
echo "backdoor" >> file.txt
git add file.txt
git commit -m "Minor kernel optimization"
git log --format="%an <%ae> - %s"
```

Look at the log. **Both commits appear equally legitimate.** Git accepted the fake identity without question.

There is no verification. No warning. No check. Anyone with write access to a repository can commit as anyone else.

## Step 4: Think about what this means

- A disgruntled developer could make commits that look like they came from someone else
- A compromised CI system could inject code under a trusted developer's name
- In a code review, you'd see "Linus Torvalds" and might trust it more than you should
- Git platforms like GitLab and Gitea render the **avatar based on the email address** - a forged commit will show the real person's profile picture, making the impersonation even more convincing

## Now you're ready

Go back to your home directory and start the investigation:

```
cd ~
cat instructions.md
```

The `signing-project/` repo has a mix of signed and unsigned commits. Some of them may not be from who they claim. Your job is to find out which ones are real.

The `cheatsheet.md` has all the commands you'll need.
