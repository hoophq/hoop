(ns webapp.components.stepper)

(defn- complete-circle
  []
  [:span {:class "relative z-10 w-8 h-8 flex items-center justify-center bg-indigo-600 rounded-full group-hover:bg-indigo-800"}
   [:svg {:class "w-5 h-5 text-white"
          :xmlns "http://www.w3.org/2000/svg"
          :viewBox "0 0 20 20"
          :fill "currentColor"}
    [:path {:fill-rule "evenodd"
            :d "M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z"
            :clip-rule "evenodd"}]]])

(defn- current-circle
  []
  [:span {:class "relative z-10 w-8 h-8 flex items-center justify-center bg-white border-2 border-indigo-600 rounded-full"}
   [:span {:class "h-2.5 w-2.5 bg-indigo-600 rounded-full"}]])

(defn- upcoming-circle
  []
  [:span {:class "relative z-10 w-8 h-8 flex items-center justify-center bg-white border-2 border-gray-300 rounded-full group-hover:border-gray-400"}
   [:span {:class "h-2.5 w-2.5 bg-transparent rounded-full bg-gray-300 group-hover:bg-gray-400"}]])

(defn step
  "Element from: https://tailwindui.com/components/application-ui/navigation/steps#component-fe94b9131ea11970b4653b2f0d0c83cd
  step -> {}, the couple of data which will determine the step.
  hash-last-step -> string, it is a hash compounding by title and status string together.
  
  step is compounding by:
   status -> enum string (:current :complete :upcoming), the status of the step.
   title -> string, the title showed in front of step
   text -> string, the text showed below the title"
  [{:keys [status title text]} hash-last-step]
  (let [last-step? (= hash-last-step (str title status))
        active-step? (not (= status "upcoming"))]
    ^{:key title} [:ol {:role "list"
                        :class "overflow-hidden border-none"}
                   [:li {:class "relative pb-10"}
                    (when (not last-step?)
                      [:div {:class (str "-ml-px absolute mt-0.5 top-4 left-4 w-0.5 h-full " (if active-step? "bg-indigo-600" "bg-gray-300"))
                             :aria-hidden "true"}])
                    [:a {:href "#"
                         :class "relative flex items-start group"}
                     [:span {:class "h-9 flex items-center"}
                      (case status
                        "complete" [complete-circle]
                        "current" [current-circle]
                        "upcoming" [upcoming-circle])]
                     [:span {:class "ml-4 min-w-0 flex flex-col"}
                      [:span {:class "text-xs font-semibold tracking-wide uppercase"}
                       title]
                      [:span {:class "text-sm text-gray-500"}
                       text]]]]]))

(defn main
  "This function crafts the stepper with steps.
  stepper -> {(keyword step-name) {:status enum :title string :text string }}, the stepper is the dictionary of steps.
  
  Each step inside of steppers is compounding by:
   status -> enum string (:current :complete :upcoming), the status of the step.
   title -> string, the title showed in front of step
   text -> string, the text showed below the title
   
  e.g {:agent {:status :complete :title 'your agent' :text 'setup your agent'}}"
  [stepper]
  (let [steps (vals stepper)
        last-step (last steps)
        hash-last-step (str (:title last-step) (:status last-step))]
    (map #(step % hash-last-step) steps)))
