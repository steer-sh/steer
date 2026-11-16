// JavaScript using const and let
function modernFunction() {
  const userName = 'john';
  let userAge = 25;
  
  if (userAge > 18) {
    const canVote = true;
    let status = 'adult';
  }
  
  for (let i = 0; i < 10; i++) {
    const item = `item_${i}`;
    console.log(item);
  }
  
  const result = {
    name: userName,
    age: userAge,
    voting: canVote
  };
  
  return result;
}