(ns webapp.components.flip-card)

(defn main
  "This component is a flip card animated. When your mouse hover on the card it active a flip animation to see what is on its back

   parameters:
   HashMap: {
     :comp-front (component reagent) -> it's what will show up in the front of the card;
     :comp-back (component reagent) -> it's what will show up in the back of the card;
     :on-click (function) -> execute the function in the on click action;
     :disabled? (boolean) -> became the card disabled with opacity;
   }"
  [{:keys [comp-front comp-back on-click disabled?]}]
  [:div {:class "col-span-1 cursor-pointer w-full h-28 bg-transparent group"
         :style {:perspective "1000px"}
         :on-click (when-not disabled? on-click)}
   [:div {:class "relative w-full h-full text-center transform duration-500 rounded-lg border shadow-0 hover:shadow group-hover:rotate-y-180"
          :style {:transform-style "preserve-3d"}}
    [:div {:class (str (when disabled? "opacity-25 ")
                       "flex items-center align-center justify-center p-6 space-x-6 absolute w-full h-full backface-visibility-hidden")}
     comp-front]
    [:div {:class (str (when disabled? "opacity-25 ")
                       "flex items-center align-center justify-center p-6 space-x-6 bg-white "
                       "absolute w-full h-full transform rotate-y-180 backface-visibility-hidden")}
     comp-back]]])
