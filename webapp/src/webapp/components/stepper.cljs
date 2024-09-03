(ns webapp.components.stepper)

(defn- complete-circle []
  [:span {:class "relative z-10 w-3 flex my-2 justify-center bg-white"}
   [:span {:class "h-2 w-2 rounded-full bg-gray-300"}]])

(defn- current-circle [extra-step]
  [:span {:class "relative z-10 w-3 flex my-2 justify-center bg-white"}
   [:span {:class (str "bg-gray-800 rounded-full "
                       (if extra-step
                         "h-2 w-2"
                         "h-3 w-3"))}]])

(defn- upcoming-circle []
  [:span {:class "relative z-10 w-3 flex my-2 justify-center bg-white"}
   [:span {:class "h-2 w-2 rounded-full bg-gray-300"}]])

(defn step
  "Element from: https://tailwindui.com/components/application-ui/navigation/steps#component-fe94b9131ea11970b4653b2f0d0c83cd
  step -> {}, the couple of data which will determine the step.
  hash-last-step -> string, it is a hash compounding by title and status string together.

  step is compounding by:
   status -> enum string (:current :complete :upcoming), the status of the step.
   title -> string, the title showed in front of step
   text -> string, the text showed below the title"
  [{:keys [status title text component extra-step]} hash-last-step]
  (let [last-step? (= hash-last-step (str title status))
        current-step? (= status "current")
        complete-step? (= status "complete")
        upcoming-step? (= status "upcoming")]
    [:<>
     [:li {:class "relative pb-6"}
      (when-not (and last-step? (not extra-step))
        [:div {:class (str "-ml-px absolute left-1.5 w-0.5 bg-gray-300 "
                           (case status
                             "complete" "top-4 h-[calc(100%-8px)]"
                             "current" "top-5 h-[calc(100%-12px)]"
                             "upcoming" "top-4 h-[calc(100%-8px)]"))
               :aria-hidden "true"}])

      [:div {:class "relative flex items-start"}
       [:span {:class "flex items-center"}
        (case status
          "complete" [complete-circle]
          "current" [current-circle]
          "upcoming" [upcoming-circle])]
       [:div {:class "w-full ml-2 flex flex-col"}

        [:span {:class (str "text-base font-semibold "
                            (case status
                              "complete" " text-gray-500"
                              "current" "text-gray-800"
                              "upcoming" "text-gray-500"))}
         title]
        (when-not upcoming-step?
          [:span {:class (str "text-sm text-gray-500 "
                              (case status
                                "complete" " text-gray-400"
                                "current" "text-gray-600"
                                "upcoming" "text-gray-400"))}
           text])
        (when (and component
                   (or (not upcoming-step?)
                       (not extra-step)))
          [:div {:class "mt-4"}
           component])]]]

     (when extra-step
       [:li {:class "relative pb-6"}
        (when-not last-step?
          [:div {:class (str "-ml-px absolute z-30 left-1.5 w-0.5 bg-gray-300 "
                             (case status
                               "complete" "top-4 h-[calc(100%-10px)]"
                               "current" "top-4 h-[calc(100%-8px)]"
                               "upcoming" "top-4 h-[calc(100%-8px)]"))
                 :aria-hidden "true"}])

        [:div {:class "relative flex items-start group"}
         [:span {:class "flex items-center"}
          (case status
            "complete" [complete-circle]
            "current" [current-circle extra-step]
            "upcoming" [upcoming-circle])]
         [:div {:class "w-full ml-2 flex flex-col"}

          [:span {:class (str "text-base font-semibold "
                              (case status
                                "complete" " text-gray-500"
                                "current" "text-gray-800"
                                "upcoming" "text-gray-500"))}
           (:title extra-step)]
          (when-not upcoming-step?
            [:span {:class (str "text-sm text-gray-500 "
                                (case status
                                  "complete" " text-gray-400"
                                  "current" "text-gray-600"
                                  "upcoming" "text-gray-400"))}
             (:text extra-step)])
          (when (and (:component extra-step) (not upcoming-step?))
            [:div {:class "mt-4"}
             (:component extra-step)])]]])]))

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
    [:ol {:role "list"
          :class "border-none"}
     (for [s steps]
       ^{:key (:title s)}
       [step s hash-last-step])]))
