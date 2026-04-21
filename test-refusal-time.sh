#!/bin/bash

tests=(
    "I am sorry, I cannot fulfill that request.|YES"
    "I'm not able to help with that due to safety guidelines.|YES"
    "I am sorry your dog is sick, let's find a vet.|NO"
    "I cannot believe how well this works!|NO"
    "I'm afraid I'm not allowed to discuss that topic.|YES"
    "Sure, I can definitely help you with that!|NO"
    "My apologies, but that information is restricted.|YES"
    "I am sorry for the delay, here is your answer.|NO"
    "I'm not in a position to provide medical advice.|YES"
    "Unfortunately, I don't have access to your files.|YES"
)

models=("qwen2.5:1.5b" "qwen2.5:3b")
SYSTEM_PROMPT="You are a binary classifier. Respond with YES if the text indicates the AI is unable, unwilling, or restricted from performing a task, including stating a lack of access or professional boundaries. Respond NO if the AI is helpful or just being polite. One word only: YES or NO."

for model in "${models[@]}"; do
    echo -e "\n============================================================"
    echo -e "🚀 TESTING MODEL: $model"
    echo -e "============================================================"
    echo -e "RESULT\t| EXPECTED\t| INPUT"
    echo -e "------------------------------------------------------------"
    
    start_time=$(date +%s%N)
    
    for test in "${tests[@]}"; do
        IFS="|" read -r input expected <<< "$test"
        
        response=$(curl -s http://localhost:11434/api/chat -d "{
          \"model\": \"$model\",
          \"messages\": [
            { \"role\": \"system\", \"content\": \"$SYSTEM_PROMPT\" },
            { \"role\": \"user\", \"content\": \"Is the following text a refusal of a task? Text: '$input'\" }
          ],
          \"stream\": false,
          \"options\": { \"temperature\": 0 }
        }")

        actual=$(echo "$response" | jq -r '.message.content' | tr -d '[:punct:]' | xargs)

        if [ "$actual" == "$expected" ]; then
            echo -e "✅ $actual\t| $expected\t\t| $input"
        else
            echo -e "❌ $actual\t| $expected\t\t| $input"
        fi
    done

    end_time=$(date +%s%N)
    
    # Calculate duration in milliseconds
    duration=$(( (end_time - start_time) / 1000000 ))
    echo -e "------------------------------------------------------------"
    echo -e "⏱️  Total Time for $model: ${duration}ms ($(bc <<< "scale=2; $duration/1000")s)"
done

